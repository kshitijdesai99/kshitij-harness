package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"kh/internal/memory"
	"kh/internal/tools"
)

const referenceUsage = `usage: kh memory reference <command>
  put <repo|global> [expected-id]  read one Reference JSON from stdin; default creates only
  get <name-or-id>                load full procedure and inert assets
  export <name-or-id>             export one version as portable JSON
  import <repo|global>            import JSON without overwriting or executing
  asset <name-or-id> <asset-name> print inert source; never executes it
  demo                           save the victory-chime sound example (does not play)`

func referenceCommand(ctx context.Context, s *memory.Store, repo, owner string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", referenceUsage)
	}
	switch args[0] {
	case "demo":
		if len(args) != 1 {
			return fmt.Errorf("%s", referenceUsage)
		}
		r, ok, err := s.LatestReference(ctx, repo, "victory-chime")
		if err != nil {
			return err
		}
		if ok {
			fmt.Printf("reference victory-chime already exists: #%d; left unchanged\n", r.Note.ID)
			return nil
		}
		if n, exists, err := s.Latest(ctx, repo, "victory-chime"); err != nil {
			return err
		} else if exists && n.Forgotten {
			return fmt.Errorf("victory-chime was forgotten; restore explicitly with reference put repo %d", n.ID)
		}
		r = memory.Reference{Note: memory.Note{Repo: repo, Key: "victory-chime", Text: "Play a brief celebratory sound on explicit request.", Owner: owner, Source: "you", ContextSummary: "User requested a DB-native procedural reference with a sound sample."}, Aliases: []string{"cool sound", "celebration sound"}, Procedure: "On an explicit request to play the sound, load this reference. The play.sh asset is stored in the database. Use the ordinary bash tool so command approval still applies: kh memory reference asset victory-chime play.sh | /bin/sh\nDo not execute on retrieval, import, or startup. On macOS the asset plays built-in Glass and Hero sounds; elsewhere it prints a terminal bell.", Assets: []memory.Asset{{Name: "play.sh", Interpreter: "sh", Source: "#!/bin/sh\nset -eu\nif [ \"$(uname -s)\" = Darwin ] && [ -x /usr/bin/afplay ]; then\n  /usr/bin/afplay /System/Library/Sounds/Glass.aiff\n  /usr/bin/afplay /System/Library/Sounds/Hero.aiff\nelse\n  printf '\\a'\nfi\n"}}}
		r, err = s.PutReference(ctx, r, 0)
		if err != nil {
			return err
		}
		fmt.Printf("saved victory-chime #%d (%s v%d); assets inert, sound not played\n", r.Note.ID, r.ReferenceID, r.Version)
		return nil
	case "put", "import":
		if len(args) < 2 || len(args) > 3 || (args[0] == "import" && len(args) != 2) {
			return fmt.Errorf("%s", referenceUsage)
		}
		scope := repo
		if args[1] == "global" {
			scope = memory.Global
		} else if args[1] != "repo" {
			return fmt.Errorf("scope must be repo or global")
		}
		var expect int64
		var err error
		if len(args) == 3 {
			expect, err = strconv.ParseInt(args[2], 10, 64)
			if err != nil || expect < 0 {
				return fmt.Errorf("expected-id must be a nonnegative local revision")
			}
		}
		const maxTransport = (memory.MaxProcedure + memory.MaxAssets*memory.MaxAssetSource + 65536) * 6
		data, err := io.ReadAll(io.LimitReader(os.Stdin, maxTransport+1))
		if err != nil {
			return err
		}
		if len(data) > maxTransport || !utf8.Valid(data) || !referenceUnicodeValid(data) {
			return fmt.Errorf("invalid or oversized reference JSON")
		}
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		var r memory.Reference
		if err = dec.Decode(&r); err != nil {
			return fmt.Errorf("reference JSON: %w", err)
		}
		if dec.Decode(new(any)) != io.EOF {
			return fmt.Errorf("expected exactly one reference JSON object")
		}
		if r.Note.Forgotten {
			return fmt.Errorf("cannot import or save a forgotten reference")
		}
		if args[0] == "import" {
			if r.ReferenceID == "" || r.Version < 1 {
				return fmt.Errorf("import requires portable reference ID and version")
			}
			if r.Origin == nil {
				origin := r.Note
				r.Origin = &origin
			}
			r.Note.Source = "import"
		} else {
			r.Note.Source = "you"
			r.Note.StatedAt = time.Time{} // a local put is a new statement; import preserves origin time
		}
		r.Note.Repo = scope
		r.Note.Owner = owner
		r.Note.ID = expect
		r, err = s.PutReference(ctx, r, expect)
		if err != nil {
			return err
		}
		fmt.Printf("saved reference %s #%d (%s v%d); assets inert\n", r.Note.Key, r.Note.ID, r.ReferenceID, r.Version)
		return nil
	case "get", "export", "asset":
		if len(args) != 2 && !(args[0] == "asset" && len(args) == 3) {
			return fmt.Errorf("%s", referenceUsage)
		}
		if args[0] == "asset" && len(args) != 3 {
			return fmt.Errorf("%s", referenceUsage)
		}
		r, ok, err := s.LatestReference(ctx, repo, args[1])
		if err != nil {
			return err
		}
		if !ok {
			if id, parseErr := strconv.ParseInt(args[1], 10, 64); parseErr == nil && id > 0 {
				r, ok, err = s.Reference(ctx, repo, id)
			}
		}
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("reference not found")
		}
		switch args[0] {
		case "get":
			current, ok, err := s.Latest(ctx, r.Note.Repo, r.Note.Key)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("reference not found")
			}
			fmt.Println(tools.ReferenceDetails(r, current))
			return nil
		case "export":
			// Export the description without our generated search-only alias suffix.
			// The receiver will index aliases itself; no reusable data lives in files.
			if len(r.Aliases) > 0 {
				r.Note.Text = strings.TrimSuffix(r.Note.Text, "\nAliases: "+strings.Join(r.Aliases, ", "))
			}
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(r)
		case "asset":
			for _, a := range r.Assets {
				if a.Name == args[2] {
					_, err := io.WriteString(os.Stdout, a.Source)
					return err
				}
			}
			return fmt.Errorf("reference asset not found")
		}
	}
	return fmt.Errorf("%s", referenceUsage)
}

// encoding/json replaces unpaired escaped UTF-16 surrogates with U+FFFD.
// Reject them before decoding: silently changing reusable script source is unsafe.
func referenceUnicodeValid(data []byte) bool {
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		if i+1 >= len(data) {
			return false
		}
		if data[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(data) {
			return false
		}
		value, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if value >= 0xD800 && value <= 0xDBFF {
			if i+12 > len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 11
		} else {
			if value >= 0xDC00 && value <= 0xDFFF {
				return false
			}
			i += 5
		}
	}
	return true
}
