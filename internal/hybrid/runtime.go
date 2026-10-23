// Package hybrid manages the opt-in local memory retrieval services. It has no
// init side effects: callers must only invoke it when hybrid mode is requested.
package hybrid

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	DefaultEndpoint   = "http://127.0.0.1:11435/rerank"
	RerankerModel     = "Qwen/Qwen3-Reranker-0.6B"
	EmbeddingEndpoint = "http://127.0.0.1:11434"
	EmbeddingModel    = "qwen3-embedding:0.6b"
	// Longer than the prototype's 64-byte secrets so old binaries cannot
	// authenticate and replace a helper started by this runtime.
	helperSecretPrefix = "kh-helper-v1:"
)

//go:embed reranker.py
var helper string

// HelperRevision changes with every embedded helper edit, including its prompt.
var HelperRevision = fmt.Sprintf("%x", sha256.Sum256([]byte(helper)))

var client = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Ensure explicitly installs Python dependencies and downloads both models, then
// starts the services, explicitly upgrading an authenticated managed helper if
// its revision differs. No binary installer is run: Python 3 and Ollama must exist.
// Downloads/installations have a 30 minute overall limit and honor cancellation.
func Ensure(ctx context.Context, progress func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return locked(ctx, true, func(root string) error {
		report := func(s string) {
			if progress != nil {
				progress(s)
			}
		}
		if err := ollama(ctx, root); err != nil {
			return err
		}
		report("Downloading Ollama " + EmbeddingModel)
		if err := pull(ctx, report); err != nil {
			return err
		}
		python := filepath.Join(root, "venv", "bin", "python")
		if _, err := os.Stat(python); err != nil {
			report("Creating private Python environment")
			system, err := exec.LookPath("python3")
			if err != nil {
				return errors.New("hybrid setup requires Python 3.10+ with venv support")
			}
			if err := run(ctx, root, system, "-m", "venv", filepath.Join(root, "venv")); err != nil {
				return err
			}
		}
		report("Installing local reranker dependencies (torch and transformers)")
		if err := run(ctx, root, python, "-m", "pip", "install", "--disable-pip-version-check", "torch>=2.6,<3", "transformers>=4.51,<5"); err != nil {
			return err
		}
		report("Downloading " + RerankerModel)
		if err := run(ctx, root, python, "-u", "-c", helper, "--download", filepath.Join(root, "cache")); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "ready"), []byte(RerankerModel), 0600); err != nil {
			return err
		}
		report("Starting local reranker (cold loading can take about a minute)")
		return start(ctx, root, true)
	})
}

// Start reuses or starts the cached local services, without pip or model pulls.
// It never replaces a different revision: only explicit Ensure may upgrade it.
// A cold model load may take up to 90 seconds. Missing setup is an explicit error.
func Start(ctx context.Context) error {
	return locked(ctx, false, func(root string) error {
		if err := ollama(ctx, root); err != nil {
			return err
		}
		return start(ctx, root, false)
	})
}

func locked(ctx context.Context, create bool, fn func(string) error) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".kh", "hybrid")
	if create {
		if err := os.MkdirAll(root, 0700); err != nil {
			return err
		}
		if err := os.Chmod(root, 0700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(root, "runtime.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("hybrid runtime is not set up; run /memory hybrid on: %w", err)
	}
	defer f.Close()
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(root)
}

type helperHealth struct {
	Model     string `json:"model"`
	Ready     bool   `json:"ready"`
	Revision  string `json:"revision"`
	Proof     string `json:"proof"`
	challenge string
	managed   bool
}

func signature(secret []byte, message string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func nonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func health(ctx context.Context, root string) (*helperHealth, error) {
	challenge, err := nonce()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(DefaultEndpoint, "/rerank")+"/health", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-KH-Challenge", challenge)
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return nil, nil
		}
		return nil, fmt.Errorf("local reranker health unavailable: %w", err)
	}
	defer res.Body.Close()
	var h helperHealth
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&h) != nil || h.Model != RerankerModel || !h.Ready {
		return nil, errors.New("port 11435 is occupied by a service that is not the expected Qwen reranker")
	}
	h.challenge = challenge
	if secret, err := os.ReadFile(filepath.Join(root, "helper.secret")); err == nil && validHelperSecret(secret) {
		h.managed = hmac.Equal([]byte(h.Proof), []byte(signature(secret, "health:"+challenge+":"+h.Revision)))
	}
	return &h, nil
}

// Legacy secrets remain verifiable for explicit upgrades, but are never minted.
func validHelperSecret(secret []byte) bool {
	value := strings.TrimPrefix(string(secret), helperSecretPrefix)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func start(ctx context.Context, root string, allowUpgrade bool) error {
	h, err := health(ctx, root)
	if err != nil {
		return err
	}
	if h != nil && h.Revision == HelperRevision && h.managed {
		return nil
	}
	if h != nil && (!h.managed || h.Revision == "") {
		return errors.New("local reranker helper is outdated or unmanaged and cannot be safely upgraded; stop the service on port 11435 manually, then run /memory hybrid on")
	}
	if h != nil && !allowUpgrade {
		return errors.New("local reranker helper revision differs from this build; run /memory hybrid on to explicitly upgrade it")
	}
	marker, err := os.ReadFile(filepath.Join(root, "ready"))
	if err != nil || string(marker) != RerankerModel {
		return errors.New("local reranker cache is not set up; run /memory hybrid on")
	}
	python := filepath.Join(root, "venv", "bin", "python")
	if _, err := exec.LookPath(python); err != nil {
		return fmt.Errorf("local reranker interpreter is unavailable: %w", err)
	}
	if h != nil {
		if err := stopHelper(ctx, root, h); err != nil {
			return err
		}
	}
	secret, err := nonce()
	if err != nil {
		return err
	}
	secret = helperSecretPrefix + secret
	secretPath := filepath.Join(root, "helper.secret")
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		return err
	}
	if err := os.Chmod(secretPath, 0600); err != nil {
		return err
	}
	cmd := exec.Command(python, "-u", "-c", helper, "--serve", filepath.Join(root, "cache"))
	cmd.Env = append(os.Environ(), "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "KH_RERANKER_REVISION="+HelperRevision, "KH_RERANKER_SECRET="+secret)
	done, err := detach(cmd, filepath.Join(root, "reranker.log"))
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		if current, err := health(wait, root); err != nil {
			// Our child binds before cold loading. Its health request can time
			// out while loading, but an unrelated listener must never be treated
			// as a free port by the initial health check.
			var timeout interface{ Timeout() bool }
			if !errors.As(err, &timeout) || !timeout.Timeout() || wait.Err() != nil {
				_ = cmd.Process.Kill()
				return err
			}
		} else if current != nil {
			if current.Revision == HelperRevision && current.managed {
				return nil
			}
			_ = cmd.Process.Kill()
			return errors.New("local reranker startup found an unexpected helper revision")
		}
		select {
		case err := <-done:
			return fmt.Errorf("local reranker exited (%v); see %s", err, filepath.Join(root, "reranker.log"))
		default:
		}
		if err := pause(wait); err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("local reranker startup: %w; see %s", err, filepath.Join(root, "reranker.log"))
		}
	}
}

// Stop only a listener that proved possession of this managed root's secret.
// No saved PID is read or signaled; old/unmanaged helpers require manual action.
func stopHelper(ctx context.Context, root string, h *helperHealth) error {
	secret, err := os.ReadFile(filepath.Join(root, "helper.secret"))
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(wait, http.MethodPost, strings.TrimSuffix(DefaultEndpoint, "/rerank")+"/shutdown", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-KH-Challenge", h.challenge)
	req.Header.Set("Authorization", "Bearer "+signature(secret, "shutdown:"+h.challenge+":"+h.Revision))
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("local reranker upgrade shutdown: %w", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("local reranker refused managed shutdown: HTTP %d", res.StatusCode)
	}
	for {
		current, err := health(wait, root)
		if err == nil {
			if current == nil {
				return nil
			}
			if !current.managed {
				return errors.New("local reranker listener changed during upgrade; refusing to replace it")
			}
		}
		// A draining server may reset a queued health connection. Wait for
		// confirmed connection refusal before launching any replacement.
		if err := pause(wait); err != nil {
			return fmt.Errorf("local reranker did not stop for upgrade: %w", err)
		}
	}
}

func ollama(ctx context.Context, root string) error {
	available := func() bool {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, EmbeddingEndpoint+"/api/tags", nil)
		res, err := client.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		var tags struct {
			Models []json.RawMessage `json:"models"`
		}
		return res.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tags) == nil && tags.Models != nil
	}
	if available() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	binary := filepath.Join(home, ".kh", "ollama-runtime", "ollama")
	if _, err := os.Stat(binary); err != nil {
		binary, err = exec.LookPath("ollama")
		if err != nil {
			return errors.New("hybrid setup requires Ollama: install Ollama or place its binary at ~/.kh/ollama-runtime/ollama")
		}
	}
	cmd := exec.Command(binary, "serve")
	// Share Ollama's normal model store (or the user's OLLAMA_MODELS override)
	// instead of creating a second store that the desktop app cannot see.
	cmd.Env = append(os.Environ(), "OLLAMA_HOST=127.0.0.1:11434")
	done, err := detach(cmd, filepath.Join(root, "ollama.log"))
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		if available() {
			return nil
		}
		select {
		case err := <-done:
			return fmt.Errorf("Ollama exited (%v); see %s", err, filepath.Join(root, "ollama.log"))
		default:
		}
		if err := pause(wait); err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("Ollama startup: %w", err)
		}
	}
}

func pull(ctx context.Context, progress func(string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, EmbeddingEndpoint+"/api/pull", strings.NewReader(`{"model":"`+EmbeddingModel+`","stream":true}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Pulls are intentionally longer than health checks, but still deadline bounded.
	c := &http.Client{Timeout: 25 * time.Minute, Transport: client.Transport, CheckRedirect: client.CheckRedirect}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("Ollama embedding pull: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama embedding pull: HTTP %d", res.StatusCode)
	}
	scan := bufio.NewScanner(res.Body)
	scan.Buffer(make([]byte, 4096), 1<<20)
	success := false
	last := ""
	for scan.Scan() {
		var p struct {
			Status    string `json:"status"`
			Error     string `json:"error"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
		}
		if err := json.Unmarshal(scan.Bytes(), &p); err != nil {
			return fmt.Errorf("invalid Ollama pull progress: %w", err)
		}
		if p.Error != "" {
			return fmt.Errorf("Ollama embedding pull: %s", p.Error)
		}
		message := p.Status
		if p.Total > 0 {
			message += fmt.Sprintf(" %d%%", int(float64(p.Completed)/float64(p.Total)*100))
		}
		if message != last {
			progress(message)
			last = message
		}
		success = p.Status == "success"
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("Ollama embedding pull: %w", err)
	}
	if !success {
		return errors.New("Ollama embedding pull ended without success")
	}
	return nil
}

func protectedLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func run(ctx context.Context, root, binary string, args ...string) error {
	log, err := protectedLog(filepath.Join(root, "setup.log"))
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(), "HF_HUB_DISABLE_TELEMETRY=1", "PIP_CACHE_DIR="+filepath.Join(root, "pip-cache"))
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("hybrid setup failed: %w; see %s", err, log.Name())
	}
	return nil
}

func detach(cmd *exec.Cmd, path string) (<-chan error, error) {
	log, err := protectedLog(path)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start local service: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, nil
}

func pause(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
