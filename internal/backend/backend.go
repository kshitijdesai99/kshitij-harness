// Package backend selects adapters without coupling the provider contract to them.
package backend

import (
	"context"
	"fmt"
	"os"

	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/provider/codex"
	"kh/internal/tools"
)

// New is the only backend-selection point. New models within a backend use
// Config.Model; a different API needs a new adapter, not changes to the loop.
func New(c config.Config, repoMap string, ts []tools.Tool, out provider.Output) (provider.Provider, error) {
	if parent := os.Getenv("KH_PARENT"); parent != "" {
		for _, tool := range ts {
			if tool.Name == "agents" {
				c.System += "\nYour address is " + os.Getenv("KH_AGENT") + "; parent: " + parent + ". Use the native agents tool to coordinate directly with peers and send results to your parent."
				break
			}
		}
	}
	switch c.Provider {
	case "codex":
		return codex.New(c, repoMap, ts, out), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", c.Provider)
	}
}

func Login(ctx context.Context, backend string) error {
	switch backend {
	case "codex":
		return codex.Login(ctx)
	default:
		return fmt.Errorf("unsupported provider %q", backend)
	}
}
