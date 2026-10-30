// Explicit workspace actions. Inspection is inert; preparation and decision
// recording are the only actions that write, confined to the resolved root.
package projectconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"kh/internal/tools"
)

type workspaceArgs struct {
	Action       string `json:"action"`
	Directory    string `json:"directory"`
	Root         string `json:"root"`
	DecisionPath string `json:"decision_path"`
	Title        string `json:"title"`
	Rationale    string `json:"rationale"`
	Affected     string `json:"affected"`
	Verification string `json:"verification"`
	Status       string `json:"status"`
}

// Serializes size checks and appends across tool instances in this process.
var workspaceWrites sync.Mutex

func workspaceTool() tools.Tool {
	return tools.Tool{
		Name:        "workspace",
		Description: "Inspect an existing target folder's applicable guidance, capability metadata and bounded decisions without writing or executing MCP/scripts. Before work, explicitly prepare to create .agents at the Git/workspace root (never each subdirectory). Record meaningful dated decisions only, with rationale, affected areas and verification/outstanding work; distinguish proposed, implemented and superseded decisions. Never record secrets, transcripts or routine logs. Use decision_path to respect an existing Markdown decision convention; inspect it first. Writes are explicit, not triggered by inspection.",
		Params: map[string]any{
			"action":        map[string]any{"type": "string", "enum": []string{"inspect", "prepare", "record"}},
			"directory":     map[string]any{"type": "string", "description": "Existing target directory; nested guidance applies here."},
			"root":          map[string]any{"type": "string", "description": "Optional explicit existing non-Git workspace root containing directory. Git targets always use the nearest Git root. Otherwise the nearest ancestor with .agents, or directory itself, is used."},
			"decision_path": map[string]any{"type": "string", "description": "Optional root-relative Markdown file for an existing decision convention, defaults to .agents/decisions.md. Parent must exist; no traversal or symlinks."},
			"title":         map[string]any{"type": "string", "description": "Decision heading (record)."},
			"rationale":     map[string]any{"type": "string", "description": "Why the decision was made (record)."},
			"affected":      map[string]any{"type": "string", "description": "Affected areas/files (record)."},
			"verification":  map[string]any{"type": "string", "description": "Verification evidence and outstanding work; do not claim unrun checks (record)."},
			"status":        map[string]any{"type": "string", "enum": []string{"proposed", "implemented", "superseded"}, "description": "Decision state (record); when superseding, identify the prior decision in rationale."},
		},
		Required: []string{"action", "directory"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var args workspaceArgs
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("workspace input: %w", err)
			}
			if args.Action != "inspect" && args.Action != "prepare" && args.Action != "record" {
				return "", fmt.Errorf("unknown workspace action %q", args.Action)
			}
			directory, err := workspaceDirectory(args.Directory)
			if err != nil {
				return "", err
			}
			root, err := workspaceRoot(directory, args.Root)
			if err != nil {
				return "", err
			}
			path, err := decisionPath(args.DecisionPath)
			if err != nil {
				return "", err
			}
			if args.Action == "inspect" {
				return inspectWorkspace(ctx, directory, root, path)
			}
			workspaceWrites.Lock()
			defer workspaceWrites.Unlock()
			if err := ctx.Err(); err != nil {
				return "", err
			}
			// Pin the root and reject pre-existing symlink components. os.Root also
			// prevents symlink swaps from redirecting writes outside this root.
			if err := safeAssetPath(root, root); err != nil {
				return "", err
			}
			handle, err := os.OpenRoot(root)
			if err != nil {
				return "", err
			}
			defer handle.Close()
			if args.Action == "prepare" {
				if err := handle.Mkdir(".agents", 0700); err != nil && !os.IsExist(err) {
					return "", err
				}
				info, err := handle.Lstat(".agents")
				if err != nil {
					return "", err
				}
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return "", fmt.Errorf(".agents must be a real directory")
				}
				return workspaceJSON(map[string]string{"root": root, "agents": filepath.Join(root, ".agents")})
			}
			entry, err := decisionEntry(args)
			if err != nil {
				return "", err
			}
			info, err := handle.Lstat(".agents")
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("prepare the workspace first: .agents must be a real directory")
			}
			full := filepath.Join(root, path)
			if err := safeAssetPath(filepath.Dir(full), root); err != nil {
				return "", err
			}
			info, err = handle.Lstat(path)
			if err == nil {
				if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
					return "", fmt.Errorf("decision file must be a regular, non-symlink file")
				}
				local := &loader{config: &Config{}}
				data, ok := local.read(full)
				if !ok {
					return "", fmt.Errorf("cannot append decisions: %s", strings.Join(local.config.Warnings, "; "))
				}
				if len(data)+len(entry) > maxFile {
					return "", fmt.Errorf("decision file would exceed 512 KiB; choose a new decision_path matching the local convention")
				}
			} else if !os.IsNotExist(err) {
				return "", err
			}
			file, err := handle.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				return "", err
			}
			_, writeErr := file.WriteString(entry)
			closeErr := file.Close()
			if writeErr != nil {
				return "", writeErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			return workspaceJSON(map[string]string{"root": root, "decision_path": full, "status": args.Status})
		},
	}
}

func workspaceDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("workspace directory is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace target is not a directory")
	}
	return absolute, nil
}

func workspaceRoot(directory, explicit string) (string, error) {
	gitRoot, agentsRoot := "", ""
	for path := directory; ; path = filepath.Dir(path) {
		if info, err := os.Lstat(filepath.Join(path, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			gitRoot = path
			break
		}
		if agentsRoot == "" {
			if info, err := os.Lstat(filepath.Join(path, ".agents")); err == nil && info.IsDir() {
				agentsRoot = path
			}
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	if explicit != "" {
		root, err := workspaceDirectory(explicit)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, directory)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("workspace root must contain target directory")
		}
		if gitRoot != "" && root != gitRoot {
			return "", fmt.Errorf("Git workspace root is %s", gitRoot)
		}
		return root, nil
	}
	if gitRoot != "" {
		return gitRoot, nil
	}
	if agentsRoot != "" {
		return agentsRoot, nil
	}
	return directory, nil
}

func decisionPath(path string) (string, error) {
	if path == "" {
		return filepath.Join(".agents", "decisions.md"), nil
	}
	if filepath.IsAbs(path) || filepath.Ext(path) != ".md" {
		return "", fmt.Errorf("decision_path must be root-relative Markdown")
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." || part == ".git" || sensitive(part) {
			return "", fmt.Errorf("unsafe decision_path")
		}
	}
	return filepath.Clean(path), nil
}

func decisionEntry(args workspaceArgs) (string, error) {
	if args.Status != "proposed" && args.Status != "implemented" && args.Status != "superseded" {
		return "", fmt.Errorf("record status must be proposed, implemented or superseded")
	}
	for _, field := range []struct{ name, value string }{{"title", args.Title}, {"rationale", args.Rationale}, {"affected", args.Affected}, {"verification", args.Verification}} {
		if strings.TrimSpace(field.value) == "" || len(field.value) > 16*1024 || !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) {
			return "", fmt.Errorf("record %s must be nonempty UTF-8 text, at most 16 KiB", field.name)
		}
	}
	if strings.ContainsAny(args.Title, "\r\n") {
		return "", fmt.Errorf("decision title must be a single line")
	}
	return fmt.Sprintf("\n\n## %s — %s\n\n- Status: %s\n- Rationale: %s\n- Affected: %s\n- Verification / outstanding: %s\n", time.Now().Format("2006-01-02"), strings.TrimSpace(args.Title), args.Status, strings.TrimSpace(args.Rationale), strings.TrimSpace(args.Affected), strings.TrimSpace(args.Verification)), nil
}

func inspectWorkspace(ctx context.Context, directory, root, path string) (string, error) {
	c, err := Load(directory)
	if err != nil {
		return "", err
	}
	catalog, err := c.Tools()[0].Run(ctx, json.RawMessage(`{"action":"search"}`))
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, path)
	decisions := ""
	if _, err := os.Lstat(full); !os.IsNotExist(err) {
		if err := safeAssetPath(full, root); err != nil {
			c.Warnings = append(c.Warnings, err.Error())
		} else {
			local := &loader{config: &Config{}}
			if data, ok := local.read(full); ok {
				decisions = string(data)
			}
			c.Warnings = append(c.Warnings, local.config.Warnings...)
		}
	}
	return workspaceJSON(struct {
		Directory    string          `json:"directory"`
		Root         string          `json:"root"`
		Guidance     string          `json:"guidance"`
		Catalog      json.RawMessage `json:"catalog"`
		DecisionPath string          `json:"decision_path"`
		Decisions    string          `json:"decisions"`
		Warnings     []string        `json:"warnings,omitempty"`
	}{directory, root, c.Prompt, json.RawMessage(catalog), full, decisions, c.Warnings})
}

func workspaceJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
