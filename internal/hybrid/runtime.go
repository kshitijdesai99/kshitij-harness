// Package hybrid manages the opt-in local memory retrieval services. It has no
// init side effects: callers must only invoke it when hybrid mode is requested.
package hybrid

import (
	"bufio"
	"context"
	_ "embed"
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
)

//go:embed reranker.py
var helper string

var client = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Ensure explicitly installs Python dependencies and downloads both models, then
// starts the services. No binary installer is run: Python 3 and Ollama must exist.
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
		return start(ctx, root)
	})
}

// Start reuses or starts the cached local services, without pip or model pulls.
// A cold model load may take up to 90 seconds. Missing setup is an explicit error.
func Start(ctx context.Context) error {
	return locked(ctx, false, func(root string) error {
		if err := ollama(ctx, root); err != nil {
			return err
		}
		return start(ctx, root)
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

func health(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(DefaultEndpoint, "/rerank")+"/health", nil)
	if err != nil {
		return false, err
	}
	res, err := client.Do(req)
	if err != nil {
		return false, ctx.Err()
	}
	defer res.Body.Close()
	var h struct {
		Model string `json:"model"`
		Ready bool   `json:"ready"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&h) != nil || h.Model != RerankerModel || !h.Ready {
		return false, errors.New("port 11435 is occupied by a service that is not the expected Qwen reranker")
	}
	return true, nil
}

func start(ctx context.Context, root string) error {
	ready, err := health(ctx)
	if err != nil || ready {
		return err
	}
	marker, err := os.ReadFile(filepath.Join(root, "ready"))
	if err != nil || string(marker) != RerankerModel {
		return errors.New("local reranker cache is not set up; run /memory hybrid on")
	}
	python := filepath.Join(root, "venv", "bin", "python")
	cmd := exec.Command(python, "-u", "-c", helper, "--serve", filepath.Join(root, "cache"))
	cmd.Env = append(os.Environ(), "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1")
	done, err := detach(cmd, filepath.Join(root, "reranker.log"))
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		if ready, err := health(wait); err != nil {
			_ = cmd.Process.Kill()
			return err
		} else if ready {
			return nil
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
