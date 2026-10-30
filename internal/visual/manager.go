package visual

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Options is application wiring, never model-supplied command configuration.
type Options struct {
	Home           string
	Guardian       []string
	UV             string
	StartupTimeout time.Duration
}

type Server struct {
	Name     string `json:"name"`
	Notebook string `json:"notebook"`
	Log      string `json:"log"`
	URL      string `json:"url,omitempty"`
	Running  bool   `json:"running"`
}
type owned struct {
	server Server
	pipe   *os.File
	done   chan struct{}
}

type Manager struct {
	mu          sync.Mutex
	root, cache string
	options     Options
	servers     map[string]*owned
	closed      bool
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("visual directory must not be a symlink: %s", path)
	}
	return nil
}

func New(options Options) (*Manager, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	// Resolve only the existing kh parent; never follow artifacts/cache aliases
	// that could accidentally grant access to credentials or unrelated files.
	base := filepath.Join(home, ".kh")
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, err
	}
	for _, path := range []string{filepath.Join(base, "artifacts"), filepath.Join(base, "cache"), filepath.Join(base, "cache", "visual")} {
		if err := privateDir(path); err != nil {
			return nil, err
		}
	}
	root, err := os.MkdirTemp(filepath.Join(base, "artifacts"), "visual-")
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(base, "cache", "visual")
	for _, name := range []string{"tmp", "config", "state"} {
		if err := privateDir(filepath.Join(root, name)); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"uv", "python", "tools", "data", "pycache", "matplotlib"} {
		if err := privateDir(filepath.Join(cache, name)); err != nil {
			return nil, err
		}
	}
	if len(options.Guardian) == 0 {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		options.Guardian = []string{executable, GuardianArgument}
	}
	if options.UV == "" {
		options.UV = "uv"
	}
	if options.StartupTimeout <= 0 {
		options.StartupTimeout = 90 * time.Second
	}
	return &Manager{root: root, cache: cache, options: options, servers: make(map[string]*owned)}, nil
}
func (m *Manager) Root() string  { return m.root }
func (m *Manager) Cache() string { return m.cache }

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func (m *Manager) notebook(name string) (string, error) {
	if !validName.MatchString(name) {
		return "", fmt.Errorf("name must contain 1-64 letters, digits, underscores or hyphens, starting with a letter or digit")
	}
	return filepath.Join(m.root, name+".py"), nil
}
func (m *Manager) Create(name string) (Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Server{}, fmt.Errorf("visual manager is closed")
	}
	path, err := m.notebook(name)
	if err != nil {
		return Server{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Server{}, err
	}
	_, err = f.WriteString("# /// script\n# dependencies = [\"marimo\"]\n# ///\nimport marimo\n\napp = marimo.App()\n\n@app.cell\ndef _():\n    import marimo as mo\n    mo.md(\"# Visual companion\")\n    return\n\nif __name__ == \"__main__\":\n    app.run()\n")
	closeErr := f.Close()
	if err != nil {
		return Server{}, err
	}
	if closeErr != nil {
		return Server{}, closeErr
	}
	return Server{Name: name, Notebook: path, Log: filepath.Join(m.root, name+".log")}, nil
}
func (m *Manager) environment() []string {
	values := map[string]string{"UV_CACHE_DIR": filepath.Join(m.cache, "uv"), "UV_PYTHON_INSTALL_DIR": filepath.Join(m.cache, "python"), "UV_TOOL_DIR": filepath.Join(m.cache, "tools"), "XDG_CACHE_HOME": m.cache, "XDG_DATA_HOME": filepath.Join(m.cache, "data"), "XDG_CONFIG_HOME": filepath.Join(m.root, "config"), "XDG_STATE_HOME": filepath.Join(m.root, "state"), "TMPDIR": filepath.Join(m.root, "tmp"), "PYTHONPYCACHEPREFIX": filepath.Join(m.cache, "pycache"), "MPLCONFIGDIR": filepath.Join(m.cache, "matplotlib")}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := values[key]; !ok {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

// Start is intentionally constrained to uv/marimo.
// The turn context controls startup only, never an already running server.
func (m *Manager) Start(ctx context.Context, name string, port int) (Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Server{}, fmt.Errorf("visual manager is closed")
	}
	if err := ctx.Err(); err != nil {
		return Server{}, err
	}
	path, err := m.notebook(name)
	if err != nil {
		return Server{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Server{}, err
	}
	if !info.Mode().IsRegular() {
		return Server{}, fmt.Errorf("notebook must be a regular file")
	}
	if port != 0 && (port < 1024 || port > 65535) {
		return Server{}, fmt.Errorf("port must be 1024-65535, or omitted")
	}
	if old := m.servers[name]; old != nil {
		select {
		case <-old.done:
			old.pipe.Close()
			delete(m.servers, name)
		default:
			return old.server, nil
		}
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return Server{}, err
	}
	port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	uv, err := exec.LookPath(m.options.UV)
	if err != nil {
		return Server{}, fmt.Errorf("visual requires uv on PATH; install uv first: %w", err)
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return Server{}, err
	}
	token := hex.EncodeToString(secret)
	// Let uv resolve notebook dependencies directly. marimo --sandbox would
	// detach the actual server into another session, defeating group ownership.
	argv := []string{uv, "run", "--no-project", "--with", "marimo", "--with-requirements", path, "marimo", "run", path, "--watch", "--no-sandbox", "--headless", "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--token-password", token}
	server := Server{Name: name, Notebook: path, Log: filepath.Join(m.root, name+".log"), URL: fmt.Sprintf("http://127.0.0.1:%d/?access_token=%s", port, token), Running: true}
	log, err := os.OpenFile(server.Log, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return Server{}, err
	}
	defer log.Close()
	ownerRead, ownerWrite, err := os.Pipe()
	if err != nil {
		return Server{}, err
	}
	defer ownerRead.Close()
	cmd := exec.Command(m.options.Guardian[0], m.options.Guardian[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{ownerRead}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		ownerWrite.Close()
		return Server{}, err
	}
	ownerRead.Close()
	item := &owned{server: server, pipe: ownerWrite, done: make(chan struct{})}
	go func() { cmd.Wait(); close(item.done) }()
	fail := func(cause error) (Server, error) {
		ownerWrite.Close()
		<-item.done
		data, _ := os.ReadFile(server.Log)
		if len(data) > 4096 {
			data = data[len(data)-4096:]
		}
		return Server{}, fmt.Errorf("visual start failed (%w); log %s: %s", cause, server.Log, data)
	}
	if err := json.NewEncoder(ownerWrite).Encode(launch{Argv: argv, Dir: m.root, Env: m.environment()}); err != nil {
		return fail(err)
	}
	// Guardian exit and authenticated HTTP response cover launch failure and
	// slow dependency installation without binding lifetime to a turn context.
	deadline := time.NewTimer(m.options.StartupTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	client := http.Client{Timeout: 300 * time.Millisecond, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	for {
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-item.done:
			return fail(fmt.Errorf("uv/marimo exited; check dependency installation and notebook syntax"))
		case <-deadline.C:
			return fail(fmt.Errorf("startup timed out; check uv installation/dependencies"))
		case <-tick.C:
			response, err := client.Get(server.URL)
			if err == nil {
				response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 400 {
					if err := ctx.Err(); err != nil {
						return fail(err)
					}
					m.servers[name] = item
					return server, nil
				}
			}
		}
	}
}
func (m *Manager) List() []Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []Server{}
	for _, item := range m.servers {
		server := item.server
		select {
		case <-item.done:
			server.Running = false
		default:
		}
		result = append(result, server)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid visual name")
	}
	item := m.servers[name]
	if item == nil {
		return fmt.Errorf("no owned server named %q", name)
	}
	item.pipe.Close()
	<-item.done
	delete(m.servers, name)
	return nil
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for name, item := range m.servers {
		item.pipe.Close()
		<-item.done
		delete(m.servers, name)
	}
	return nil
}
