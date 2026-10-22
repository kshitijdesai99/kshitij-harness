package hybrid_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"kh/internal/hybrid"
)

func home(t *testing.T, setup bool) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	root := filepath.Join(h, ".kh", "hybrid")
	if setup {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func server(t *testing.T, address string, handler http.Handler) bool {
	t.Helper()
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	s := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go s.Serve(ln)
	t.Cleanup(func() { s.Close() })
	return true
}

// Reuse an existing Ollama only for read-only Start tests; never pull against it.
func ollamaFixture(t *testing.T, requireMock bool) {
	t.Helper()
	if server(t, "127.0.0.1:11434", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[]}`))
		case "/api/pull":
			var body struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != hybrid.EmbeddingModel || !body.Stream {
				t.Error("invalid pull request")
			}
			w.Write([]byte("{\"status\":\"pulling\",\"total\":100,\"completed\":50}\n{\"status\":\"success\"}\n"))
		default:
			t.Errorf("unexpected Ollama path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})) {
		return
	}
	if requireMock {
		t.Skip("11434 occupied; refusing to pull from an existing service")
	}
	res, err := (&http.Client{Timeout: time.Second}).Get(hybrid.EmbeddingEndpoint + "/api/tags")
	if err != nil {
		t.Skip("11434 unavailable for mock Ollama")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Skip("11434 is not an available Ollama")
	}
}

func rerankerFixture(t *testing.T, model string) {
	t.Helper()
	if !server(t, "127.0.0.1:11435", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"model": model, "ready": true})
	})) {
		t.Skip("11435 occupied; refusing to interfere with existing reranker")
	}
}

func TestStartMissingSetupHasNoSideEffects(t *testing.T) {
	root := home(t, false)
	if err := hybrid.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("Start created setup directory: %v", err)
	}
}

func TestCancellationDoesNotRunSetup(t *testing.T) {
	root := home(t, true)
	t.Setenv("PATH", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, call := range []func() error{func() error { return hybrid.Start(ctx) }, func() error { return hybrid.Ensure(ctx, nil) }} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "venv")); !os.IsNotExist(err) {
		t.Fatal("cancelled setup created venv")
	}
}

func TestStartReusesOnlyMatchingModel(t *testing.T) {
	for _, model := range []string{hybrid.RerankerModel, "wrong-model"} {
		t.Run(model, func(t *testing.T) {
			home(t, true)
			ollamaFixture(t, false)
			rerankerFixture(t, model)
			t.Setenv("PATH", t.TempDir())
			err := hybrid.Start(context.Background())
			if model == hybrid.RerankerModel && err != nil {
				t.Fatal(err)
			}
			if model != hybrid.RerankerModel && (err == nil || !strings.Contains(err.Error(), "not the expected")) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestStartCachedOnlyAndProtectedLogs(t *testing.T) {
	root := home(t, true)
	ollamaFixture(t, false)
	// Reserve/check the port before testing a failing fake detached process.
	ln, err := net.Listen("tcp", "127.0.0.1:11435")
	if err != nil {
		t.Skip("11435 occupied")
	}
	ln.Close()
	if err := os.MkdirAll(filepath.Join(root, "venv", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\nprintf 'OFFLINE=%s/%s\\n' \"$HF_HUB_OFFLINE\" \"$TRANSFORMERS_OFFLINE\"\nexit 7\n"
	if err := os.WriteFile(filepath.Join(root, "venv", "bin", "python"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ready"), []byte(hybrid.RerankerModel), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hybrid.Start(ctx); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("error = %v", err)
	}
	log := filepath.Join(root, "reranker.log")
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\n--serve\n") || !strings.Contains(string(b), "OFFLINE=1/1") {
		t.Fatalf("unexpected startup command log: %s", b)
	}
	// The embedded helper text itself mentions download; only actual argv must use --serve.
	if strings.Contains(string(b), "\n--download\n") || strings.Contains(string(b), "\n-m\npip\n") {
		t.Fatal("Start attempted a download")
	}
	info, err := os.Stat(log)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("log protection: %v %v", info, err)
	}
}

func TestStartDetachedServiceSurvivesCallerContext(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 unavailable")
	}
	root := home(t, true)
	ollamaFixture(t, false)
	ln, err := net.Listen("tcp", "127.0.0.1:11435")
	if err != nil {
		t.Skip("11435 occupied")
	}
	ln.Close()
	if err := os.MkdirAll(filepath.Join(root, "venv", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	code := `import json, os, time
from http.server import HTTPServer, BaseHTTPRequestHandler
print(os.getpid(), flush=True)
time.sleep(0.1)
class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_): pass
    def do_GET(self):
        body = json.dumps({"model": "Qwen/Qwen3-Reranker-0.6B", "ready": True}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
HTTPServer(("127.0.0.1", 11435), Handler).serve_forever()
`
	script := "#!/bin/sh\nexec " + strconv.Quote(python) + " -u - <<'PY'\n" + code + "\nPY\n"
	if err := os.WriteFile(filepath.Join(root, "venv", "bin", "python"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ready"), []byte(hybrid.RerankerModel), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hybrid.Start(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "reranker.log"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		deadline := time.Now().Add(time.Second)
		for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	})
	cancel()
	time.Sleep(20 * time.Millisecond)
	response, err := (&http.Client{Timeout: time.Second}).Get("http://127.0.0.1:11435/health")
	if err != nil {
		t.Fatalf("detached service died with caller: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("health status %d", response.StatusCode)
	}
	// Subsequent Start must reuse it, even with the interpreter removed.
	if err := os.Remove(filepath.Join(root, "venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}
	if err := hybrid.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartCancellationKillsColdProcess(t *testing.T) {
	root := home(t, true)
	ollamaFixture(t, false)
	ln, err := net.Listen("tcp", "127.0.0.1:11435")
	if err != nil {
		t.Skip("11435 occupied")
	}
	ln.Close()
	if err := os.MkdirAll(filepath.Join(root, "venv", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	// exec keeps the owned PID stable, so cancellation must reap this exact process.
	if err := os.WriteFile(filepath.Join(root, "venv", "bin", "python"), []byte("#!/bin/sh\necho $$\nexec sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ready"), []byte(hybrid.RerankerModel), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logPath := filepath.Join(root, "reranker.log")
	observed := make(chan struct{})
	go func() {
		defer close(observed)
		for ctx.Err() == nil {
			if b, _ := os.ReadFile(logPath); len(b) > 0 {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	err = hybrid.Start(ctx)
	<-observed
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("cancelled cold child %d still exists: %v", pid, err)
	}
}

func TestEnsureWithMockDependencies(t *testing.T) {
	root := home(t, true)
	ollamaFixture(t, true)
	rerankerFixture(t, hybrid.RerankerModel)
	bin := t.TempDir()
	// Fake python creates the private interpreter; both pip/model commands merely
	// record their argv. No installation or network access occurs in this fixture.
	script := "#!/bin/sh\nif [ \"$1\" = -m ] && [ \"$2\" = venv ]; then\n mkdir -p \"$3/bin\"\n cp \"$0\" \"$3/bin/python\"\nelse\n printf '%s\\n' \"$@\"\nfi\n"
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var progress []string
	if err := hybrid.Ensure(context.Background(), func(s string) { progress = append(progress, s) }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "setup.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\ninstall\n") || !strings.Contains(string(b), "\n--download\n") {
		t.Fatalf("missing explicit setup steps: %s", b)
	}
	if !strings.Contains(strings.Join(progress, "\n"), "50%") {
		t.Fatalf("missing pull progress: %v", progress)
	}
	if b, err := os.ReadFile(filepath.Join(root, "ready")); err != nil || string(b) != hybrid.RerankerModel {
		t.Fatalf("ready marker %q: %v", b, err)
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private directory permissions %v: %v", info, err)
	}
}

func TestPythonHelper(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 unavailable")
	}
	cmd := exec.Command(python, "-B", "helper_test.py")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper tests: %v\n%s", err, b)
	}
}
