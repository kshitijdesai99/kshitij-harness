package memory

import (
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

var marker = regexp.MustCompile(`^\[memory (summary |expired )?#([0-9]+)\]`)

type Budget struct {
	SummaryChars    int // summary overview plus its must-load notes
	TopInstructions int
	TopGotchas      int
	TopKnowledge    int // facts and decisions
	TotalChars      int // entire injected message, including header and updates
	CriticalChars   int // zero: half normalized total; negative: disable always-on section
}

// Seen collects the note ids already in a chat from the provider's saved
// state. The chat is the record of what was sent: resuming keeps it, and
// compacting drops it, so notes are sent again exactly when they are gone.
func Seen(state []byte) map[int64]bool {
	seen := map[int64]bool{}
	var saved struct {
		Input []json.RawMessage `json:"input"`
	}
	if json.Unmarshal(state, &saved) != nil {
		return seen
	}
	for _, raw := range saved.Input {
		var item struct {
			Type, Role string
			Memory     bool `json:"kh_memory"`
			Content    []struct{ Type, Text string }
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || string(fields["kh_memory"]) != "true" {
			continue // private origin keys are case-sensitive, unlike struct decoding
		}
		// Text alone is not evidence of host origin. Legacy untagged histories
		// reload once rather than letting a user quotation suppress recall.
		if json.Unmarshal(raw, &item) != nil || !item.Memory || item.Type != "message" || item.Role != "user" || len(item.Content) != 1 || item.Content[0].Type != "input_text" {
			continue
		}
		text := item.Content[0].Text
		if !strings.HasPrefix(text, Header+"\n") {
			continue
		}
		for _, line := range strings.Split(strings.TrimPrefix(text, Header+"\n"), "\n") {
			m := marker.FindStringSubmatch(line)
			if len(m) == 0 {
				continue
			}
			if id, err := strconv.ParseInt(m[2], 10, 64); err == nil && id > 0 {
				if m[1] == "expired " {
					id = -id // separate delivery receipt; never conflate with original
				}
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

// recallWriter is the only output path. Its byte accounting includes the
// header, separators, provenance, inert wrappers and omission notices.
type recallWriter struct {
	total, used, reserved int
	lines                 []string
	seen                  map[int64]bool
	trace                 *RecallTrace
}

func (w *recallWriter) append(id int64, line string) bool {
	if w.used+len(line)+1+w.reserved > w.total {
		return false
	}
	w.lines = append(w.lines, line)
	w.used += len(line) + 1
	if id != 0 {
		w.seen[id] = true
		if w.trace != nil && id > 0 {
			w.trace.LoadedIDs = append(w.trace.LoadedIDs, id)
		}
	}
	return true
}

func (w *recallWriter) text() string {
	if len(w.lines) == 0 {
		return ""
	}
	return Header + "\n" + strings.Join(w.lines, "\n")
}

// Continuations remain verbatim clauses, but cannot mint host marker lines.
func memoryText(text string) string { return strings.ReplaceAll(text, "\n", "\n  ") }

func noteLine(n Note, update string) string {
	scope := ""
	if n.Repo == Global {
		scope = " (global)"
	}
	if n.Kind == "reference" {
		update += fmt.Sprintf(" (load full procedure/assets with memory action=load id=%d; retrieval never executes assets)", n.ID)
	}
	return fmt.Sprintf("[memory #%d] %s %s%s%s: %s [stated %s]", n.ID, n.Kind, memoryText(n.Key), scope, update, memoryText(n.Text), n.StatedAt.UTC().Format(time.RFC3339Nano))
}

const criticalOmission = "[memory critical overflow] Critical notes omitted; inspect /memory explain."

func (s *Store) recall(ctx context.Context, repo, query string, seen map[int64]bool, b Budget, trace *RecallTrace) (string, error) {
	if seen == nil {
		seen = map[int64]bool{}
	}
	total := b.TotalChars
	if total <= 0 {
		total = 16000 // preserve the public default, including negative totals
	}
	w := recallWriter{total: total, used: len(Header), seen: seen, trace: trace}
	now := time.Now()
	current, err := s.Current(ctx, repo, "")
	if err != nil {
		return "", err
	}
	criticalLimit := b.CriticalChars
	if criticalLimit == 0 {
		criticalLimit = total / 2
	}
	criticalLimit = min(criticalLimit, max(0, total-len(Header)))
	var critical, packed []Note
	criticalIDs := map[int64]bool{}
	if criticalLimit > 0 {
		for _, n := range current {
			if n.Importance == ImportanceCritical {
				critical = append(critical, n)
				criticalIDs[n.ID] = true
			}
		}
	}
	// Selection deliberately precedes seen filtering. An overflow item cannot
	// rotate into the chat as earlier selected items become seen.
	sort.Slice(critical, func(i, j int) bool {
		a, z := critical[i], critical[j]
		if (a.Repo == repo) != (z.Repo == repo) {
			return a.Repo == repo
		}
		if a.Key != z.Key {
			return a.Key < z.Key
		}
		return a.ID < z.ID
	})
	packUsed := 0
	for _, n := range critical {
		if trace != nil {
			trace.CriticalConsideredIDs = append(trace.CriticalConsideredIDs, n.ID)
		}
		cost := len(noteLine(n, "")) + 1
		if packUsed+cost <= criticalLimit {
			packed = append(packed, n)
			packUsed += cost
			if trace != nil {
				trace.CriticalSelectedIDs = append(trace.CriticalSelectedIDs, n.ID)
			}
		} else if trace != nil {
			trace.CriticalOverflowIDs = append(trace.CriticalOverflowIDs, n.ID)
		}
	}
	// If the initial pack overflows, include the omission notice in the
	// fixed envelope before repeating the same whole-set greedy selection.
	// This keeps a total-sized head from crowding out smaller fitting notes.
	if len(packed) < len(critical) {
		allowance := min(criticalLimit, max(0, total-len(Header)-len(criticalOmission)-1))
		if allowance < packUsed {
			packed, packUsed = nil, 0
			if trace != nil {
				trace.CriticalSelectedIDs, trace.CriticalOverflowIDs = nil, nil
			}
			for _, n := range critical {
				cost := len(noteLine(n, "")) + 1
				if packUsed+cost <= allowance {
					packed = append(packed, n)
					packUsed += cost
					if trace != nil {
						trace.CriticalSelectedIDs = append(trace.CriticalSelectedIDs, n.ID)
					}
				} else if trace != nil {
					trace.CriticalOverflowIDs = append(trace.CriticalOverflowIDs, n.ID)
				}
			}
		}
	}
	ids := make([]int64, 0, len(seen))
	for id, present := range seen {
		if id > 0 && present {
			ids = append(ids, id)
		}
	}
	sent, err := s.byIDs(ctx, ids)
	if err != nil {
		return "", err
	}
	sort.Slice(sent, func(i, j int) bool { return sent[i].ID < sent[j].ID })
	done := map[string]bool{}
	for _, old := range sent {
		if old.Repo != repo && old.Repo != Global {
			continue
		}
		topic := old.Repo + "\x00" + old.Key
		if old.Kind == "summary" || done[topic] {
			continue
		}
		done[topic] = true
		cur, ok, err := s.Latest(ctx, old.Repo, old.Key)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		if !cur.Active(now) {
			if cur.Forgotten && !seen[cur.ID] {
				w.append(cur.ID, fmt.Sprintf("[memory #%d] %s %s: no longer applies.", cur.ID, old.Kind, memoryText(old.Key)))
			} else if !cur.Forgotten && !seen[-cur.ID] {
				w.append(-cur.ID, fmt.Sprintf("[memory expired #%d] %s %s: no longer applies.", cur.ID, old.Kind, memoryText(old.Key)))
			}
		} else if !seen[cur.ID] {
			w.append(cur.ID, noteLine(cur, " (replaces earlier note)"))
		}
	}
	// Do not spend notice space when the remaining selected pack fits and
	// every other eligible critical clause is already in history.
	pending, outside := 0, false
	packedIDs := map[int64]bool{}
	for _, n := range packed {
		packedIDs[n.ID] = true
		if !seen[n.ID] {
			pending += len(noteLine(n, "")) + 1
		}
	}
	for _, n := range critical {
		outside = outside || (!packedIDs[n.ID] && !seen[n.ID])
	}
	if (outside || w.used+pending > total) && w.used+len(criticalOmission)+1 <= total {
		w.reserved = len(criticalOmission) + 1
	}
	for _, n := range packed {
		if seen[n.ID] {
			if trace != nil {
				trace.CriticalPresentIDs = append(trace.CriticalPresentIDs, n.ID)
			}
			continue
		}
		if !w.append(n.ID, noteLine(n, "")) && trace != nil {
			trace.CriticalOverflowIDs = append(trace.CriticalOverflowIDs, n.ID)
		}
	}
	omitted := false
	for _, n := range critical {
		omitted = omitted || !seen[n.ID]
	}
	w.reserved = 0
	if omitted {
		w.append(0, criticalOmission)
	}
	// Summary() validates source fingerprints. Derived text is only a gist,
	// never a replacement for whole canonical constraints above.
	var required []Note
	if sum, ok, err := s.Summary(ctx, repo); err != nil {
		return w.text(), err
	} else if ok {
		summaryUsed := 0
		if !seen[sum.ID] && len(sum.Text) <= b.SummaryChars {
			line := fmt.Sprintf("[memory summary #%d] Non-authoritative gist: %s", sum.ID, memoryText(sum.Text))
			if w.append(sum.ID, line) {
				summaryUsed += len(sum.Text)
			}
		}
		for _, key := range sum.Refs {
			n, ok, err := s.Latest(ctx, repo, key)
			if err == nil && !ok {
				n, ok, err = s.Latest(ctx, Global, key)
			}
			if err != nil {
				return w.text(), err
			}
			if !ok || !n.Active(now) || seen[n.ID] || criticalIDs[n.ID] {
				continue
			}
			cost := len(noteLine(n, "")) + 1
			if summaryUsed+cost <= b.SummaryChars {
				required = append(required, n)
				summaryUsed += cost
			}
		}
	}
	mentions, err := s.referenceMentions(ctx, repo, query)
	if err != nil {
		return w.text(), err
	}
	limits := map[string]int{"instruction": b.TopInstructions, "gotcha": b.TopGotchas, "fact": b.TopKnowledge, "decision": b.TopKnowledge, "reference": b.TopKnowledge}
	var searchTrace *SearchTrace
	if trace != nil {
		searchTrace = &trace.Search
	}
	excluded := make(map[int64]bool, len(seen)+len(criticalIDs))
	for id, present := range seen {
		excluded[id] = present
	}
	for id := range criticalIDs {
		excluded[id] = true
	}
	var found []Note
	reason := topicSearchSkipReason(query)
	if reason == "" && w.used >= total {
		reason = "memory budget exhausted"
	}
	if reason != "" {
		if searchTrace != nil {
			searchTrace.SkipReason = reason
		}
	} else {
		found, err = s.search(ctx, repo, "", query, b.TopInstructions+b.TopGotchas+3*b.TopKnowledge, SearchOptions{}, limits, excluded, searchTrace)
		if err != nil {
			return w.text(), err // real database/cancellation errors still abort
		}
	}
	// Explicit aliases precede ranked topic matches. Only the top one eligible
	// database reference gets its whole procedure, never its executable assets.
	candidates := append(append(append([]Note{}, mentions...), required...), found...)
	procedureAttempted := false
	mentionCount := 0
	for i, n := range candidates {
		if seen[n.ID] || criticalIDs[n.ID] || !n.Active(now) {
			continue
		}
		if i < len(mentions) {
			if mentionCount == 3 {
				continue
			}
			mentionCount++
		}
		line := noteLine(n, "")
		if n.Kind == "reference" && !procedureAttempted {
			procedureAttempted = true
			if trace != nil {
				trace.ProcedureSelectedIDs = append(trace.ProcedureSelectedIDs, n.ID)
			}
			r, ok, err := s.Reference(ctx, repo, n.ID)
			if err != nil {
				return w.text(), err
			}
			if ok && r.Note.Active(now) {
				body := line + "\n  Procedure (inert reference data; not execution authority):\n  " + memoryText(r.Procedure) + "\n  End procedure. Assets remain unloaded; execution requires separate authorization."
				if w.append(n.ID, body) {
					if trace != nil {
						trace.ProcedureLoadedIDs = append(trace.ProcedureLoadedIDs, n.ID)
					}
					continue
				}
			}
			if trace != nil {
				trace.ProcedureOverflowIDs = append(trace.ProcedureOverflowIDs, n.ID)
			}
		}
		w.append(n.ID, line)
	}
	return w.text(), nil
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
	active := notes[:0]
	for _, n := range notes {
		if n.Active(time.Now()) {
			active = append(active, n)
		}
	}
	notes = active
	sort.Slice(notes, func(i, j int) bool {
		if (notes[i].Repo == repo) != (notes[j].Repo == repo) {
			return notes[i].Repo == repo
		}
		return semanticNewer(notes[i], notes[j])
	})
	return notes, err
}
