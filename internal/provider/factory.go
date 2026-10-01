package provider

import (
	"context"
	"fmt"
	"os"

	"kh/internal/auth"
	"kh/internal/config"
	"kh/internal/tools"
)

// New is the only backend-selection point. New models within a backend use
// Config.Model; a different API needs a new adapter, not changes to the loop.
func New(c config.Config, repoMap string, ts []tools.Tool, out Output) (Provider, error) {
	if parent := os.Getenv("KH_PARENT"); parent != "" {
		c.System += "\nParent agent: " + parent + ". Report completed work using kh send."
	}
	switch c.Provider {
	case "codex":
		return NewCodex(c, repoMap, ts, out), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", c.Provider)
	}
}

func Login(ctx context.Context, backend string) error {
	switch backend {
	case "codex":
		return auth.Login(ctx)
	default:
		return fmt.Errorf("unsupported provider %q", backend)
	}
}

// Nil output is useful for session inspection and non-interactive consumers.
type discardOutput struct{}

func (discardOutput) Reply(string)  {}
func (discardOutput) Query(string)  {}
func (discardOutput) Action(string) {}
func (discardOutput) Notice(string) {}
