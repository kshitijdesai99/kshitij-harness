package memory

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"kh/internal/provider"
)

// Header starts every memory message kh adds to a chat. Seen only trusts
// markers after it, so ids quoted elsewhere (say, in a compaction summary
// that no longer holds the note text) do not count as already sent.
const Header = provider.MemoryHeader

var marker = regexp.MustCompile(`\[memory (?:summary )?#(\d+)\]`)

type Budget struct {
	SummaryChars    int // summary overview plus its must-load notes
	TopInstructions int
	TopGotchas      int
}

// Seen collects the note ids already in a chat from the provider's saved
// state. The chat is the record of what was sent: resuming keeps it, and
// compacting drops it, so notes are sent again exactly when they are gone.
func Seen(state []byte) map[int64]bool {
	seen := map[int64]bool{}
	for _, part := range bytes.Split(state, []byte(Header))[1:] {
		end := len(part)
		for i := 0; i < len(part); i++ { // the message ends at the first unescaped quote
			if part[i] == '\\' {
				i++
			} else if part[i] == '"' {
				end = i
				break
			}
		}
		for _, m := range marker.FindAllSubmatch(part[:end], -1) {
			if id, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil {
				seen[id] = true
			}
		}
	}
	return seen
}

// Recall builds the memory message for one user message, holding only what
// the chat does not have yet: the summary (once per chat), new versions of
// topics already sent, and the top matching instructions and gotchas.
// It returns "" when there is nothing new.
func (s *Store) Recall(ctx context.Context, repo, query string, seen map[int64]bool, b Budget) (string, error) {
	var lines []string
	add := func(n Note, note string) {
		scope := ""
		if n.Repo == Global {
			scope = " (global)"
		}
		lines = append(lines, fmt.Sprintf("[memory #%d] %s %s%s%s: %s", n.ID, n.Kind, n.Key, scope, note, n.Text))
		seen[n.ID] = true
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sent, err := s.byIDs(ctx, ids)
	if err != nil {
		return "", err
	}

	if sum, ok, err := s.Summary(ctx, repo); err != nil {
		return "", err
	} else if ok && !seen[sum.ID] {
		lines = append(lines, fmt.Sprintf("[memory summary #%d] %s", sum.ID, sum.Text))
		seen[sum.ID] = true
		used := len(sum.Text)
		for _, key := range sum.Refs {
			n, ok, err := s.Latest(ctx, repo, key)
			if err == nil && !ok {
				n, ok, err = s.Latest(ctx, Global, key)
			}
			if err != nil {
				return "", err
			}
			if !ok || n.Forgotten || seen[n.ID] {
				continue
			}
			if used += len(n.Text) + len(n.Key) + 30; used > b.SummaryChars {
				break
			}
			add(n, "")
		}
	}

	// A topic already in the chat may have changed since; send the change so
	// the model does not keep following the old version.
	sort.Slice(sent, func(i, j int) bool { return sent[i].ID < sent[j].ID })
	done := map[string]bool{}
	for _, old := range sent {
		topic := old.Repo + "\x00" + old.Key
		if old.Kind == "summary" || done[topic] {
			continue
		}
		done[topic] = true
		cur, ok, err := s.Latest(ctx, old.Repo, old.Key)
		if err != nil {
			return "", err
		}
		if !ok || seen[cur.ID] {
			continue
		}
		if cur.Forgotten {
			lines = append(lines, fmt.Sprintf("[memory #%d] %s %s: no longer applies.", cur.ID, old.Kind, old.Key))
			seen[cur.ID] = true
		} else {
			add(cur, " (replaces earlier note)")
		}
	}

	for _, k := range []struct {
		kind  string
		limit int
	}{{"instruction", b.TopInstructions}, {"gotcha", b.TopGotchas}} {
		found, err := s.Search(ctx, repo, k.kind, query, k.limit)
		if err != nil {
			return "", err
		}
		for _, n := range found {
			if !seen[n.ID] {
				add(n, "")
			}
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return Header + "\n" + strings.Join(lines, "\n"), nil
}
