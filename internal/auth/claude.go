// Package auth stores Claude setup tokens separately from Codex credentials.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"kh/internal/config"
)

var setupTokenRE = regexp.MustCompile(`sk-ant-oat[[:alnum:]_-]+`)

type claudeCredential struct { Token string `json:"token"` }

// LoginClaude runs Claude Code's setup-token flow and stores the resulting token.
func LoginClaude(ctx context.Context) error {
	bin, err := exec.LookPath("claude")
	if err != nil { return fmt.Errorf("Claude Code CLI not found; install it and run `claude setup-token`") }
	var out strings.Builder
	cmd := exec.CommandContext(ctx, bin, "setup-token")
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	cmd.Stdout = io.MultiWriter(os.Stdout, &out)
	if err := cmd.Run(); err != nil { return fmt.Errorf("claude setup-token: %w", err) }
	token := setupTokenRE.FindString(out.String())
	if token == "" { return fmt.Errorf("couldn't read a setup token from `claude setup-token` output") }
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil { return err }
	f, err := os.OpenFile(filepath.Join(config.Dir(), "claude.json"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil { return err }; defer f.Close()
	return json.NewEncoder(f).Encode(claudeCredential{token})
}

func ClaudeToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")); t != "" { return t, nil }
	b, err := os.ReadFile(filepath.Join(config.Dir(), "claude.json"))
	if err != nil { return "", fmt.Errorf("not logged in: run `kh login claude` (uses `claude setup-token`)") }
	var c claudeCredential
	if json.Unmarshal(b, &c) != nil || c.Token == "" { return "", fmt.Errorf("invalid Claude token; run `kh login claude` again") }
	return c.Token, nil
}
