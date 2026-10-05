package terminal

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ReadableMath converts a deliberately small subset of LaTeX math to readable
// Unicode. Unsupported or ambiguous expressions are left intact, including their
// delimiters. Markdown code spans and fenced code blocks are copied byte-for-byte.
func ReadableMath(markdown string) string {
	return readableMath(markdown, false)
}

// ReadableMathMarkdown is ReadableMath with recognized formula text escaped for
// a subsequent Markdown rendering pass. Unsupported formulas are escaped too,
// so Markdown cannot consume their LaTeX delimiters or reinterpret their contents.
// Code and ordinary prose are not escaped.
func ReadableMathMarkdown(markdown string) string {
	return readableMath(markdown, true)
}

func readableMath(markdown string, escape bool) string {
	var out strings.Builder
	protected := mathCodeRanges(markdown)
	at := 0
	for i := 0; i < len(markdown); {
		for at < len(protected) && protected[at].Stop <= i {
			at++
		}
		if at < len(protected) && protected[at].Start <= i {
			out.WriteString(markdown[i:protected[at].Stop])
			i = protected[at].Stop
			continue
		}
		if i == 0 || markdown[i-1] == '\n' {
			// Conservatively protect indented code lines, including tabs. This
			// also avoids changing math in indented list/code continuations.
			if strings.HasPrefix(markdown[i:], "    ") || markdown[i] == '\t' {
				end := strings.IndexByte(markdown[i:], '\n')
				if end < 0 {
					end = len(markdown)
				} else {
					end += i + 1
				}
				out.WriteString(markdown[i:end])
				i = end
				continue
			}
			if end := mathFenceEnd(markdown, i); end > i {
				out.WriteString(markdown[i:end])
				i = end
				continue
			}
		}
		if markdown[i] == '`' && !mathEscaped(markdown, i) {
			n := mathRun(markdown, i, '`')
			end := i + n
			matched := false
			for end < len(markdown) {
				if markdown[end] == '`' {
					m := mathRun(markdown, end, '`')
					if m == n {
						end += m
						matched = true
						break
					}
					end += m
				} else {
					end++
				}
			}
			// An unmatched backtick run is ordinary markdown, not a code span.
			if matched {
				out.WriteString(markdown[i:end])
				i = end
				continue
			}
			out.WriteString(markdown[i : i+n])
			i += n
			continue
		}
		open, close := "", ""
		if !mathEscaped(markdown, i) {
			switch {
			case strings.HasPrefix(markdown[i:], "\\("):
				open, close = "\\(", "\\)"
			case strings.HasPrefix(markdown[i:], "\\["):
				open, close = "\\[", "\\]"
			case strings.HasPrefix(markdown[i:], "$$"):
				open, close = "$$", "$$"
			case markdown[i] == '$' && mathDollarStart(markdown, i):
				open, close = "$", "$"
			}
		}
		if open != "" {
			start := i + len(open)
			for end := start; end < len(markdown); end++ {
				if close == "$" && markdown[end] == '\n' {
					break
				}
				if !strings.HasPrefix(markdown[end:], close) || mathEscaped(markdown, end) {
					continue
				}
				body := markdown[start:end]
				valid := close != "$" || mathDollarEnd(markdown, start, end)
				if at < len(protected) && protected[at].Start < end {
					valid = false
				}
				converted, ok := mathConvert(body)
				result := markdown[i : end+len(close)]
				if valid && ok {
					result = converted
				}
				if escape && valid {
					result = escapeMathMarkdown(result)
				}
				out.WriteString(result)
				i = end + len(close)
				open = "done"
				break
			}
			if open == "done" {
				continue
			}
		}
		out.WriteByte(markdown[i])
		i++
	}
	return out.String()
}

// Parsed source segments protect code inside blockquotes and lists as well as
// top-level code. The lightweight scanner below also preserves unmatched fences.
func mathCodeRanges(source string) []text.Segment {
	root := goldmark.DefaultParser().Parse(text.NewReader([]byte(source)))
	var spans []text.Segment
	ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				spans = append(spans, n.Lines().At(i))
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan:
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				if t, ok := c.(*ast.Text); ok {
					spans = append(spans, t.Segment)
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	return spans
}

func escapeMathMarkdown(s string) string {
	var out strings.Builder
	for _, r := range s {
		if (r >= '!' && r <= '/') || (r >= ':' && r <= '@') || (r >= '[' && r <= '`') || (r >= '{' && r <= '~') {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func mathRun(s string, i int, c byte) int {
	j := i
	for j < len(s) && s[j] == c {
		j++
	}
	return j - i
}

func mathEscaped(s string, i int) bool {
	n := 0
	for i--; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 != 0
}

func mathFenceEnd(s string, start int) int {
	i := start
	for i < len(s) && s[i] == ' ' && i-start < 3 {
		i++
	}
	if i >= len(s) || (s[i] != '`' && s[i] != '~') {
		return start
	}
	c := s[i]
	n := mathRun(s, i, c)
	if n < 3 {
		return start
	}
	lineEnd := strings.IndexByte(s[i+n:], '\n')
	if lineEnd < 0 {
		return len(s)
	}
	lineEnd += i + n
	if c == '`' && strings.ContainsRune(s[i+n:lineEnd], '`') {
		return start
	}
	for line := lineEnd + 1; line < len(s); {
		end := strings.IndexByte(s[line:], '\n')
		if end < 0 {
			end = len(s)
		} else {
			end += line
		}
		j := line
		for j < end && s[j] == ' ' && j-line < 3 {
			j++
		}
		if mathRun(s, j, c) >= n && strings.TrimSpace(s[j+mathRun(s, j, c):end]) == "" {
			if end < len(s) {
				end++
			}
			return end
		}
		line = end + 1
	}
	return len(s) // An unclosed fence protects the rest of the document.
}

func mathDollarStart(s string, i int) bool {
	if i+1 >= len(s) || s[i+1] == '$' || (i > 0 && s[i-1] == '$') {
		return false
	}
	next, _ := utf8.DecodeRuneInString(s[i+1:])
	if unicode.IsSpace(next) {
		return false
	}
	if i > 0 {
		prev, _ := utf8.DecodeLastRuneInString(s[:i])
		if unicode.IsLetter(prev) || unicode.IsDigit(prev) {
			return false
		}
	}
	return true
}

func mathDollarEnd(s string, start, end int) bool {
	if end == start || (end+1 < len(s) && s[end+1] == '$') {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(s[start:end])
	if unicode.IsSpace(last) {
		return false
	}
	if end+1 < len(s) {
		next, _ := utf8.DecodeRuneInString(s[end+1:])
		if unicode.IsLetter(next) || unicode.IsDigit(next) {
			return false
		}
	}
	body := s[start:end]
	// A bare amount (including decimal/thousands separators) is currency, not
	// math. Numeric expressions need an explicit operator or LaTeX syntax.
	first, _ := utf8.DecodeRuneInString(body)
	if unicode.IsDigit(first) && !strings.ContainsAny(body, "\\^_+=*/<>−×÷") {
		return false
	}
	return true
}

var mathCommands = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ε", "varepsilon": "ϵ",
	"zeta": "ζ", "eta": "η", "theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ",
	"lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ", "omicron": "ο", "pi": "π", "varpi": "ϖ",
	"rho": "ρ", "varrho": "ϱ", "sigma": "σ", "varsigma": "ς", "tau": "τ", "upsilon": "υ",
	"phi": "φ", "varphi": "ϕ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π",
	"Sigma": "Σ", "Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
	"times": "×", "cdot": "·", "div": "÷", "pm": "±", "mp": "∓", "le": "≤", "leq": "≤",
	"ge": "≥", "geq": "≥", "ne": "≠", "neq": "≠", "approx": "≈", "equiv": "≡", "sim": "∼",
	"infty": "∞", "partial": "∂", "nabla": "∇", "sum": "∑", "prod": "∏", "int": "∫",
	"in": "∈", "notin": "∉", "subset": "⊂", "subseteq": "⊆", "supset": "⊃", "supseteq": "⊇",
	"cup": "∪", "cap": "∩", "emptyset": "∅", "forall": "∀", "exists": "∃", "neg": "¬",
	"land": "∧", "lor": "∨", "to": "→", "rightarrow": "→", "leftarrow": "←",
	"leftrightarrow": "↔", "Rightarrow": "⇒", "Leftarrow": "⇐", "Leftrightarrow": "⇔",
	"ldots": "…", "cdots": "⋯", "dots": "…", "ell": "ℓ", "hbar": "ℏ",
	"sin": "sin", "cos": "cos", "tan": "tan", "log": "log", "ln": "ln", "exp": "exp",
	"lim": "lim", "min": "min", "max": "max", "quad": " ", "qquad": "  ",
}

type mathParser struct {
	s string
	i int
}

func mathConvert(s string) (string, bool) {
	if strings.TrimSpace(s) == "" || strings.ContainsRune(s, '`') {
		return "", false
	}
	p := mathParser{s: s}
	v, ok := p.sequence(false)
	return v, ok && p.i == len(s)
}

func (p *mathParser) sequence(group bool) (string, bool) {
	var out strings.Builder
	for p.i < len(p.s) {
		if p.s[p.i] == '}' {
			if !group {
				return "", false
			}
			p.i++
			return out.String(), true
		}
		if p.s[p.i] == '^' || p.s[p.i] == '_' {
			if out.Len() == 0 {
				return "", false
			}
			kind := p.s[p.i]
			p.i++
			v, ok := p.argument()
			if !ok || v == "" {
				return "", false
			}
			from, to := "0123456789+-=()aehijklmnoprstuvx", "₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎ₐₑₕᵢⱼₖₗₘₙₒₚᵣₛₜᵤᵥₓ"
			if kind == '^' {
				// There is no ordinary Unicode superscript q; never substitute
				// a look-alike character with a different mathematical meaning.
				from, to = "0123456789+-=()abcdefghijklmnoprstuvwxyz", "⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁼⁽⁾ᵃᵇᶜᵈᵉᶠᵍʰⁱʲᵏˡᵐⁿᵒᵖʳˢᵗᵘᵛʷˣʸᶻ"
			}
			mapped := []rune(to)
			for _, r := range v {
				index := strings.IndexRune(from, r)
				if index < 0 {
					return "", false
				}
				out.WriteRune(mapped[index])
			}
			continue
		}
		v, ok := p.atom()
		if !ok {
			return "", false
		}
		out.WriteString(v)
	}
	return out.String(), !group
}

func (p *mathParser) argument() (string, bool) {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\n' || p.s[p.i] == '\t') {
		p.i++
	}
	if p.i < len(p.s) && p.s[p.i] == '{' {
		p.i++
		return p.sequence(true)
	}
	return p.atom()
}

func (p *mathParser) atom() (string, bool) {
	if p.i >= len(p.s) {
		return "", false
	}
	c := p.s[p.i]
	p.i++
	if c == '{' {
		v, ok := p.sequence(true)
		// Retain grouping when braces occur outside a command argument.
		return "(" + v + ")", ok
	}
	if c == '\\' {
		if p.i >= len(p.s) {
			return "", false
		}
		start := p.i
		for p.i < len(p.s) && ((p.s[p.i] >= 'a' && p.s[p.i] <= 'z') || (p.s[p.i] >= 'A' && p.s[p.i] <= 'Z')) {
			p.i++
		}
		if start == p.i {
			p.i++
			switch p.s[start] {
			case ',', ';', ':', ' ':
				return " ", true
			case '!':
				return "", true
			case '{', '}', '%', '$', '_', '#':
				return p.s[start:p.i], true
			default:
				return "", false
			}
		}
		cmd := p.s[start:p.i]
		switch cmd {
		case "frac", "dfrac", "tfrac":
			a, ok := p.argument()
			if !ok || a == "" {
				return "", false
			}
			b, ok := p.argument()
			return "(" + a + ")/(" + b + ")", ok && b != ""
		case "sqrt":
			a, ok := p.argument()
			// Optional root indices are intentionally unsupported.
			if a == "[" {
				return "", false
			}
			return "√(" + a + ")", ok && a != ""
		default:
			v, ok := mathCommands[cmd]
			return v, ok
		}
	}
	p.i--
	r, size := utf8.DecodeRuneInString(p.s[p.i:])
	p.i += size
	if r == utf8.RuneError || strings.ContainsRune("}^_$&#~", r) || (unicode.IsControl(r) && !unicode.IsSpace(r)) {
		return "", false
	}
	return string(r), true
}
