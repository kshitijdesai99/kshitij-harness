package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	TopKnowledge    int // facts and decisions
	TotalChars      int // entire injected message, including header and updates
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
	return s.recall(ctx, repo, query, seen, b, nil)
}

// RecallWithTrace reports the actual search and newly injected notes, including
// summaries and corrections that do not come from search. It does no extra retrieval.
func (s *Store) RecallWithTrace(ctx context.Context, repo, query string, seen map[int64]bool, b Budget) (string, RecallTrace, error) {
	trace := RecallTrace{Search: SearchTrace{
		SemanticEnabled: s.semanticConfig() != nil,
		RerankEnabled:   s.rerankConfig() != nil,
	}}
	text, err := s.recall(ctx, repo, query, seen, b, &trace)
	return text, trace, err
}

func (s *Store) recall(ctx context.Context, repo, query string, seen map[int64]bool, b Budget, trace *RecallTrace) (string, error) {
	if seen == nil {
		seen = map[int64]bool{}
	}
	total := b.TotalChars
	if total <= 0 {
		total = 16000
	}
	used := len(Header)
	var lines []string
	appendLine := func(id int64, key, line string) bool {
		if used+len(line)+1 > total {
			return false
		}
		lines = append(lines, line)
		used += len(line) + 1
		seen[id] = true
		if trace != nil {
			trace.LoadedIDs = append(trace.LoadedIDs, id)
			// Keys are stored data, not trusted terminal control sequences.
			key = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return -1
				}
				return r
			}, key)
			trace.Loaded = append(trace.Loaded, fmt.Sprintf("#%d %s", id, key))
		}
		return true
	}
	add := func(n Note, note string) bool {
		scope := ""
		if n.Repo == Global {
			scope = " (global)"
		}
		if n.Kind == "reference" {
			note += fmt.Sprintf(" (load full procedure/assets with memory action=load id=%d; retrieval never executes assets)", n.ID)
		}
		return appendLine(n.ID, n.Key, fmt.Sprintf("[memory #%d] %s %s%s%s: %s [stated %s]", n.ID, n.Kind, n.Key, scope, note, n.Text, n.StatedAt.UTC().Format(time.RFC3339Nano)))
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sent, err := s.byIDs(ctx, ids)
	if err != nil {
		return "", err
	}

	// Corrections take priority over summaries and new matches. Never mark a
	// skipped note seen: it must remain eligible on the next turn.
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
			appendLine(cur.ID, old.Key, fmt.Sprintf("[memory #%d] %s %s: no longer applies.", cur.ID, old.Kind, old.Key))
		} else {
			add(cur, " (replaces earlier note)")
		}
	}
	// Explicit names/aliases remain discoverable even when the ordinary search
	// quotas are zero. Only compact metadata is read or injected here.
	mentions, err := s.referenceMentions(ctx, repo, query)
	if err != nil {
		return "", err
	}
	added := 0
	for _, n := range mentions {
		if seen[n.ID] {
			continue
		}
		if add(n, "") {
			added++
		}
		if added == 3 {
			break
		}
	}
	if sum, ok, err := s.Summary(ctx, repo); err != nil {
		return "", err
	} else if ok {
		summaryUsed := 0
		if !seen[sum.ID] {
			line := fmt.Sprintf("[memory summary #%d] %s", sum.ID, sum.Text)
			if len(sum.Text) <= b.SummaryChars && appendLine(sum.ID, sum.Key, line) {
				summaryUsed += len(sum.Text)
			}
		}
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
			cost := len(n.Text) + len(n.Key) + 30
			if summaryUsed+cost > b.SummaryChars {
				continue
			}
			if add(n, "") {
				summaryUsed += cost
			}
		}
	}
	// One hybrid search across all kinds avoids repeating query embeddings.
	limits := map[string]int{"instruction": b.TopInstructions, "gotcha": b.TopGotchas, "fact": b.TopKnowledge, "decision": b.TopKnowledge, "reference": b.TopKnowledge}
	var searchTrace *SearchTrace
	if trace != nil {
		searchTrace = &trace.Search
	}
	reason := topicSearchSkipReason(query)
	if reason == "" && used >= total {
		reason = "memory budget exhausted"
	}
	if reason != "" {
		if searchTrace != nil {
			searchTrace.SkipReason = reason
		}
	} else {
		found, err := s.search(ctx, repo, "", query, b.TopInstructions+b.TopGotchas+3*b.TopKnowledge, SearchOptions{}, limits, seen, searchTrace)
		if err != nil {
			return "", err
		}
		for _, n := range found {
			add(n, "")
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return Header + "\n" + strings.Join(lines, "\n"), nil
}

// Social units must cover the whole message. Unknown words, code, filenames,
// and mixed task requests conservatively retain ordinary retrieval.
const socialGreeting = `(?:hi|hey|hello|greetings|good (?:morning|afternoon|evening))(?: (?:there|folks|everyone|friend))?`
const socialCheckin = `(?:whats up|what is up|whatsup|sup|hows it going|how is it going|how are you(?: doing| feeling)?|hows your day(?: going)?|how is your day(?: going)?|how have you been)`
const socialAck = `(?:thanks(?: a lot| so much| very much)?|thank you(?: so much| very much)?|ok|okay|got it|sounds good|all good|understood|cool|no problem|(?:im|i am) (?:good|fine|well|okay))`
const socialUnit = `(?:` + socialGreeting + `|` + socialCheckin + `|` + socialAck + `)`

var socialMessage = regexp.MustCompile(`^` + socialUnit + `(?: (?:and )?` + socialUnit + `){0,2}$`)

// topicSearchSkipReason only gates automatic topic recall, not explicit searches,
// corrections, required summary notes, or named references. Whole-message matching
// preserves short topics and mixed requests such as "hey, fix the tests".
func topicSearchSkipReason(query string) string {
	for _, word := range strings.Fields(query) {
		if strings.Contains(strings.Trim(word, ".!?;:\"'"), ".") {
			return "" // Filenames and dotted identifiers are not small talk.
		}
	}
	var normalized strings.Builder
	for _, r := range strings.ToLower(query) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r):
			normalized.WriteRune(r)
		case r == '\'' || r == '’':
			// Match contractions both with and without an apostrophe.
		case strings.ContainsRune(".,!?;:\"", r) || unicode.Is(unicode.So, r) || r == '\uFE0F':
			normalized.WriteByte(' ')
		default:
			return "" // Code punctuation is not a conversational separator.
		}
	}
	q := strings.Join(strings.Fields(normalized.String()), " ")
	if q == "" {
		return "no topic"
	}
	if socialMessage.MatchString(q) {
		return "standalone greeting or acknowledgment"
	}
	return ""
}

// referenceMentions inspects names/aliases only, never full procedures/assets.
func (s *Store) referenceMentions(ctx context.Context, repo, query string) ([]Note, error) {
	normalize := func(text string) string {
		return " " + strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ") + " "
	}
	q := normalize(query)
	rows, err := s.db.QueryContext(ctx, latest+` SELECT l.id,l.key,p.aliases FROM latest l JOIN reference_payloads p ON p.note_id=l.id WHERE l.kind='reference' AND l.forgotten=0`, repo)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var name, raw string
		if err = rows.Scan(&id, &name, &raw); err != nil {
			break
		}
		var aliases []string
		if err = json.Unmarshal([]byte(raw), &aliases); err != nil {
			break
		}
		for _, label := range append(aliases, name) {
			phrase := normalize(label)
			if strings.TrimSpace(phrase) != "" && strings.Contains(q, phrase) {
				ids = append(ids, id)
				break
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	notes, err := s.byIDs(ctx, ids)
	sort.Slice(notes, func(i, j int) bool {
		if (notes[i].Repo == repo) != (notes[j].Repo == repo) {
			return notes[i].Repo == repo
		}
		return semanticNewer(notes[i], notes[j])
	})
	return notes, err
}
