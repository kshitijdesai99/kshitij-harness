package tools

import (
	"context"
	"os"
	"strings"
)

type bashEnvironmentKey struct{}

// WithBashEnvironment supplies per-call compatibility variables without global
// environment mutation or changes to command approval. Values are copied.
func WithBashEnvironment(ctx context.Context, values map[string]string) context.Context {
	env := make(map[string]string, len(values))
	for k, v := range values {
		env[k] = v
	}
	return context.WithValue(ctx, bashEnvironmentKey{}, env)
}

func mergeBashEnvironment(values map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}
