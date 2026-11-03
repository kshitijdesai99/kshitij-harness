package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"kh/internal/tools"
)

// These are component bytes, not provider wire sizes or tokenizer counts.
// Keep this diagnostic independent of the provider's private request format.
type bootstrapDiagnostic struct {
	core, runtime, project, repoMap, toolMetadata int
	exposed, available                            int
	resumed                                       bool
}

func newBootstrapDiagnostic(core, system, project, repoMap string, exposed []tools.Tool, available int) bootstrapDiagnostic {
	metadata := make([]map[string]any, 0, len(exposed))
	for _, tool := range exposed {
		metadata = append(metadata, map[string]any{"name": tool.Name, "description": tool.Description, "params": tool.Params, "required": tool.Required, "input_schema": tool.InputSchema})
	}
	encoded, _ := json.Marshal(metadata)
	return bootstrapDiagnostic{core: len(core), runtime: len(system) - len(core) - len(project), project: len(project), repoMap: len(repoMap), toolMetadata: len(encoded), exposed: len(exposed), available: available}
}

func (d bootstrapDiagnostic) awareness() string {
	return fmt.Sprintf("(context) %d tool wrappers; %d native/MCP operations discoverable; project guidance loaded, capabilities on demand (/project diagnostics)", d.exposed, d.available)
}

func (d bootstrapDiagnostic) warning() string {
	// Instructions cannot be silently evicted to meet the compact design target.
	floor := d.core + d.runtime + d.project
	if !d.resumed {
		floor += d.repoMap
	}
	if floor > 5000*4 {
		return "(context) mandatory instruction/map floor exceeds the ~5,000-token design target by the bytes/4 heuristic; preserved without dropping guidance. Inspect /project diagnostics."
	}
	return ""
}

func (d bootstrapDiagnostic) summary() string {
	var out strings.Builder
	mapBytes := fmt.Sprint(d.repoMap)
	floor := d.core + d.runtime + d.project + d.repoMap
	if d.resumed {
		mapBytes = "unavailable (provider-owned saved context)"
		floor -= d.repoMap
	}
	fmt.Fprintf(&out, "Startup context components (bytes, not provider tokens): core=%d runtime=%d project=%d repo_map=%s tool_metadata_json=%d; %d exposed wrappers / %d discoverable operations.\n", d.core, d.runtime, d.project, mapBytes, d.toolMetadata, d.exposed, d.available)
	fmt.Fprintf(&out, "Known instruction/map floor: %d bytes (~%d tokens by bytes/4 heuristic). Tool metadata is adapter-neutral JSON, not the wire schema. Excludes history, user input, recalled memory, provider framing and hosted tools. Actual provider input tokens appear in turn metrics.\n", floor, (floor+3)/4)
	if d.resumed {
		out.WriteString("Resume may restore additional provider-owned context; its bytes and total floor are unavailable here, not zero.\n")
	}
	if warning := d.warning(); warning != "" {
		fmt.Fprintln(&out, warning)
	}
	return out.String()
}
