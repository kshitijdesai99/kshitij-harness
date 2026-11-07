// Package pluginhooks runs explicitly trusted plugin hooks. Discovery and trust
// decisions belong to the caller; this package is not a process sandbox.
package pluginhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"kh/internal/projectconfig"
)

const (
	SessionStart     = "SessionStart"
	UserPromptSubmit = "UserPromptSubmit"
	Stop             = "Stop"
	PreToolUse       = "PreToolUse"
	SessionEnd       = "SessionEnd"
)

type Input struct {
	Event          string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	Cwd            string          `json:"cwd"`
	TranscriptPath string          `json:"transcript_path"`
	StopHookActive bool            `json:"stop_hook_active"`
	Source         string          `json:"source,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	Prompt         string          `json:"prompt,omitempty"`
}

// Context preserves the provenance of each untrusted hook output field.
// JSON encoding keeps payload text separate from the host's context framing.
type Context struct {
	Plugin string `json:"plugin"`
	Event  string `json:"event"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
}

type Result struct {
	Context     []Context
	Diagnostics []string
}

type Runner struct {
	ConfigDir      string
	MaxOutput      int
	DefaultTimeout time.Duration
}

// Environment supplies session/project compatibility values. Hook subprocesses
// pass their own plugin root; generic Bash passes empty, never a selected root.
func (r Runner) Environment(in Input, pluginRoot string) map[string]string {
	env := map[string]string{"CLAUDE_PLUGIN_ROOT": pluginRoot, "CLAUDE_PROJECT_DIR": in.Cwd, "CLAUDE_SESSION_ID": in.SessionID, "CLAUDE_CODE_SESSION_ID": in.SessionID}
	// Preserve the caller's Claude configuration/authentication, including an
	// unset CLAUDE_CONFIG_DIR. A transcript root is not a runtime config root.
	if r.ConfigDir != "" {
		env["CLAUDE_CONFIG_DIR"] = r.ConfigDir
	}
	return env
}

// Run executes only the trusted declarations supplied by its caller. Failures
// become diagnostics, never an implicit permission to block or retry a turn.
func (r Runner) Run(ctx context.Context, hooks []projectconfig.Hook, in Input) Result {
	var out Result
	switch in.Event {
	case SessionStart, UserPromptSubmit, Stop, PreToolUse, SessionEnd:
	default:
		out.Diagnostics = append(out.Diagnostics, "unsupported hook event")
		return out
	}
	data, err := json.Marshal(in)
	if err != nil {
		out.Diagnostics = append(out.Diagnostics, "encode hook input: "+err.Error())
		return out
	}
	if len(data) > 2*1024*1024 {
		out.Diagnostics = append(out.Diagnostics, "hook input exceeds limit")
		return out
	}
	for _, h := range hooks {
		if ctx.Err() != nil {
			out.Diagnostics = append(out.Diagnostics, "hook cancelled: "+ctx.Err().Error())
			break
		}
		if h.Event != in.Event {
			continue
		}
		target := in.Source
		if in.Event == PreToolUse {
			target = in.ToolName
		}
		if h.Matcher != "" && h.Matcher != "*" {
			re, e := regexp.Compile(h.Matcher)
			if e != nil {
				out.Diagnostics = append(out.Diagnostics, "invalid hook matcher")
				continue
			}
			if !re.MatchString(target) {
				continue
			}
		}
		result, diagnostic := r.runOne(ctx, h, in, data)
		if diagnostic != "" {
			out.Diagnostics = append(out.Diagnostics, diagnostic)
			continue
		}
		var response struct {
			SystemMessage      string `json:"systemMessage"`
			AdditionalContext  string `json:"additionalContext"`
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if len(bytes.TrimSpace(result)) == 0 {
			continue
		}
		if err := json.Unmarshal(result, &response); err != nil {
			out.Diagnostics = append(out.Diagnostics, "hook returned invalid JSON")
			continue
		}
		if response.SystemMessage != "" {
			out.Context = append(out.Context, Context{h.Plugin, in.Event, "systemMessage", response.SystemMessage})
		}
		for _, s := range []string{response.AdditionalContext, response.HookSpecificOutput.AdditionalContext} {
			if s != "" {
				out.Context = append(out.Context, Context{h.Plugin, in.Event, "additionalContext", s})
			}
		}
	}
	return out
}

type boundedBuffer struct {
	mu       sync.Mutex
	b        bytes.Buffer
	max      int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := b.max - b.b.Len()
	if n > left {
		b.b.Write(p[:left])
		b.exceeded = true
		b.cancel()
		return n, nil
	}
	b.b.Write(p)
	return n, nil
}
func (r Runner) runOne(ctx context.Context, h projectconfig.Hook, in Input, data []byte) ([]byte, string) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = r.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	limit := r.MaxOutput
	if limit <= 0 || limit > 64*1024 {
		limit = 64 * 1024
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := map[string]string{}
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			env[k] = v
		}
	}
	for k, v := range h.Env {
		env[k] = v
	}
	for k, v := range r.Environment(in, h.PluginRoot) {
		env[k] = v
	}
	// Expand only compatibility placeholders. Shell variables and substitutions
	// must remain shell syntax (not be consumed by os.Expand).
	expand := func(s string) string {
		for k, v := range r.Environment(in, h.PluginRoot) {
			s = strings.ReplaceAll(s, "${"+k+"}", v)
		}
		return s
	}
	command := expand(h.Command)
	args := make([]string, len(h.Args))
	for i, a := range h.Args {
		args[i] = expand(a)
		// Shell scripts expand their own environment with native quoting. Raw
		// string substitution here would turn a quoted root into shell syntax.
		if i > 0 && h.Args[i-1] == "-c" && (filepath.Base(command) == "sh" || filepath.Base(command) == "bash") {
			args[i] = a
		}
	}
	if h.RequiresNode || filepath.Base(command) == "node" {
		if _, err := exec.LookPath("node"); err != nil {
			return nil, "hook requires Node.js, but node is unavailable"
		}
	}
	if command == "" {
		return nil, "hook has no command"
	}
	cmd := exec.CommandContext(child, command, args...)
	cmd.Dir = in.Cwd
	cmd.Stdin = bytes.NewReader(data)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 200 * time.Millisecond
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	stdout := &boundedBuffer{max: limit, cancel: cancel}
	stderr := &boundedBuffer{max: limit, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, "hook output exceeded limit"
	}
	if child.Err() != nil {
		return nil, "hook cancelled or timed out: " + child.Err().Error()
	}
	if err != nil {
		return nil, fmt.Sprintf("hook command failed: %v", err)
	}
	return stdout.b.Bytes(), ""
}
