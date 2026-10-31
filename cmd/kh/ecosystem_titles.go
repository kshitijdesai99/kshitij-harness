package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"kh/internal/terminal"
)

// startLiveDescendants refreshes presentation independently of model turns. The
// returned stop waits for the poller before its Activity or registry is closed.
func (e *ecosystemRuntime) startLiveDescendants(ctx context.Context, activity *terminal.Activity) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		window, published := "", ""
		refresh := func() {
			poll, stop := context.WithTimeout(ctx, 900*time.Millisecond)
			defer stop()
			// Probe errors are conservative: Reconcile still applies proven exits,
			// leaving uncertain incarnations counted. Count errors keep the old UI.
			_ = e.reconcile(poll)
			count, parent, err := e.store.LiveDescendants(poll, e.self.ID)
			if err != nil {
				return
			}
			activity.SetLiveDescendants(count, parent)
			pane := e.self.Pane.PaneID
			if pane == "" {
				return
			}
			info, err := tmuxContext(poll, "display-message", "-p", "-t", pane, "#{@kh_agent_id}|#{window_id}")
			parts := strings.Split(info, "|")
			if err != nil || len(parts) != 2 || parts[0] != e.self.ID {
				return
			}
			value := ""
			if parent {
				value = strconv.Itoa(count)
			}
			if published != value || window != parts[1] {
				if _, err := tmuxContext(poll, "set-option", "-p", "-t", pane, "@kh_live_descendants", value); err != nil {
					return
				}
				published = value
			}
			if window != parts[1] {
				if err := decorateDescendantWindow(poll, parts[1]); err != nil {
					return
				}
				window = parts[1]
			}
		}
		refresh()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				refresh()
			}
		}
	}()
	return func() {
		cancel()
		<-done
		activity.SetLiveDescendants(0, false)
		activity.Stop()
		if pane := e.self.Pane.PaneID; pane != "" {
			cleanup, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			owner, err := tmuxContext(cleanup, "display-message", "-p", "-t", pane, "#{@kh_agent_id}")
			if err == nil && owner == e.self.ID {
				_, _ = tmuxContext(cleanup, "set-option", "-p", "-u", "-t", pane, "@kh_live_descendants")
			}
		}
	}
}

// This expression is evaluated in the window's active-pane context, so moving
// or selecting panes never lends one agent's counter to a different pane.
const descendantSuffix = "#{?#{!=:#{@kh_live_descendants},}, (#{@kh_live_descendants}),}"

func descendantWindowFormat(format string) string {
	var out strings.Builder
	depth := 0
	for i := 0; i < len(format); {
		if strings.HasPrefix(format[i:], "##") || strings.HasPrefix(format[i:], "#}") {
			out.WriteString(format[i : i+2])
			i += 2
			continue
		}
		if strings.HasPrefix(format[i:], "#(") {
			// Shell syntax can contain arbitrary braces and name tokens. Leave
			// the remaining tail intact rather than trying to parse shell code.
			out.WriteString(format[i:])
			break
		}
		token := ""
		if depth == 0 {
			if strings.HasPrefix(format[i:], "#W") {
				token = "#W"
			} else if strings.HasPrefix(format[i:], "#{window_name}") {
				token = "#{window_name}"
			}
		}
		// Nested names may be comparison operands, not displayed names.
		if token == "" && strings.HasPrefix(format[i:], "#{") {
			depth++
		} else if format[i] == '}' && depth > 0 {
			depth--
		}
		if token != "" {
			out.WriteString(token)
			out.WriteString(descendantSuffix)
			i += len(token)
		} else {
			out.WriteByte(format[i])
			i++
		}
	}
	return out.String()
}

func decorateDescendantWindow(ctx context.Context, window string) error {
	for _, option := range []string{"window-status-format", "window-status-current-format"} {
		format, err := tmuxRawContext(ctx, "show-options", "-A", "-w", "-v", "-t", window, option)
		if err != nil {
			return err
		}
		if strings.Contains(format, "@kh_live_descendants") {
			continue
		}
		// Decorate only an existing name token. Custom formats without a name
		// remain intact rather than inventing a new layout or resetting styles.
		composed := descendantWindowFormat(format)
		if composed == format {
			continue
		}
		if _, err := tmuxContext(ctx, "set-option", "-w", "-t", window, option, composed); err != nil {
			return err
		}
	}
	return nil
}
