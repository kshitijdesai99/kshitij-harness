package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"kh/internal/mcp"
)

var connectorLabel = regexp.MustCompile(`^[a-zA-Z0-9_. -]+(:[a-zA-Z0-9_. -]+)*$`)
var connectorCamel = regexp.MustCompile(`([a-z])([A-Z])`)

func connectorSummary(reports []mcp.ConnectionResult) string {
	var out strings.Builder
	out.WriteString("MCP connectors: session startup snapshot (registered availability, not live health)\n")
	if len(reports) == 0 {
		out.WriteString("No MCP servers configured for this session.\n")
	}
	ordered := append([]mcp.ConnectionResult(nil), reports...)
	rank := map[string]int{"connected": 0, "failed": 1, "not connected": 2}
	sort.SliceStable(ordered, func(i, j int) bool {
		if rank[ordered[i].Status] != rank[ordered[j].Status] {
			return rank[ordered[i].Status] < rank[ordered[j].Status]
		}
		return ordered[i].Name < ordered[j].Name
	})
	for _, report := range ordered {
		name := report.Name
		if len(name) > 80 || !connectorLabel.MatchString(name) || strings.Contains(name, "..") {
			name = "[redacted server name]"
		}
		switch report.Status {
		case "connected":
			fmt.Fprintf(&out, "- %s: connected; %d advertised tools", name, len(report.ToolNames))
			if report.Resources {
				out.WriteString("; resources available")
			}
			if report.Prompts {
				out.WriteString("; prompts available")
			}
			if hints := connectorServices(report.ToolNames); len(hints) > 0 {
				fmt.Fprintf(&out, "; exposed service hints: %s", strings.Join(hints, ", "))
			}
			out.WriteByte('\n')
		case "failed":
			fmt.Fprintf(&out, "- %s: failed (startup connection/discovery failed)\n", name)
		default:
			fmt.Fprintf(&out, "- %s: not connected (startup trust not granted or interrupted)\n", name)
		}
	}
	out.WriteString("Service hints come only from advertised tool names, not verified product authentication or permissions. No live checks performed; restart to refresh startup discovery.\n")
	return out.String()
}

func connectorServices(names []string) []string {
	labels := map[string]string{"jira": "Jira", "confluence": "Confluence", "slack": "Slack", "github": "GitHub", "gitlab": "GitLab", "cekura": "Cekura", "notion": "Notion", "salesforce": "Salesforce", "trello": "Trello", "loom": "Loom"}
	seen := map[string]bool{}
	for _, name := range names {
		// Match explicit name tokens and camel-case operation names, never descriptions,
		// server labels, URLs or generic discovery/execute tools.
		for _, spelling := range []string{name, connectorCamel.ReplaceAllString(name, "${1}_${2}")} {
			for _, token := range strings.FieldsFunc(strings.ToLower(spelling), func(r rune) bool { return r < 'a' || r > 'z' }) {
				if label, ok := labels[token]; ok {
					seen[label] = true
				}
			}
		}
	}
	var services []string
	for service := range seen {
		services = append(services, service)
	}
	sort.Strings(services)
	return services
}
