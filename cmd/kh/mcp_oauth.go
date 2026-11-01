package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/chzyer/readline"

	"kh/internal/mcp"
	"kh/internal/tools"
)

func projectOAuth(approval tools.Approver, notice func(string)) *mcp.OAuthOptions {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	opts := &mcp.OAuthOptions{StoreDir: filepath.Join(home, ".kh", "mcp", "oauth"), Notice: notice}
	printOnly := flag.Lookup("p")
	if (printOnly != nil && printOnly.Value.String() == "true") ||
		(currentEcosystem != nil && currentEcosystem.self.ParentID != "") || os.Getenv("KH_PARENT") != "" ||
		!readline.IsTerminal(int(os.Stdin.Fd())) || !readline.IsTerminal(int(os.Stdout.Fd())) {
		return opts
	}
	opts.OpenBrowser = func(ctx context.Context, authorizationURL string) error {
		u, err := url.Parse(authorizationURL)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("invalid MCP authorization destination")
		}
		// Only the origin is displayed: the authorization URL includes private
		// state and PKCE parameters. Login needs explicit consent even in auto.
		allowed, err := approval.Approve(ctx, fmt.Sprintf("Open browser for MCP sign-in at %s://%s", u.Scheme, u.Host), false)
		if err != nil {
			return errors.New("MCP sign-in confirmation cancelled")
		}
		if !allowed {
			return errors.New("MCP sign-in declined")
		}
		return openMCPBrowser(ctx, authorizationURL)
	}
	return opts
}

func openMCPBrowser(ctx context.Context, authorizationURL string) error {
	var command string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		return errors.New("MCP browser sign-in is unsupported on this platform")
	}
	// No shell, inherited output, or command error: any of these could expose
	// the authorization URL. CommandContext also bounds a stuck opener.
	if err := exec.CommandContext(ctx, command, authorizationURL).Run(); err != nil {
		return errors.New("could not open MCP sign-in browser")
	}
	return nil
}
