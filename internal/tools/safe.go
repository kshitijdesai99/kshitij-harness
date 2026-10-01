package tools

import (
	"regexp"
	"strings"
	"unicode"
)

// This is a deliberately small shell subset, not a shell parser or a security
// boundary. Unrecognized syntax/options require approval rather than guessing.
func isSafe(command string, safe []string) bool {
	if strings.ContainsAny(command, "&><`$\n\r\\(){}#") {
		// Only the explicitly supported && operator may contain ampersands.
		if strings.ContainsAny(strings.ReplaceAll(command, "&&", ""), "&><`$\n\r\\(){}#") {
			return false
		}
	}
	parts, ok := safeWords(command)
	if !ok {
		return false
	}
	for _, words := range parts {
		if !matchesAny(words, safe) || !safeOptions(words) {
			return false
		}
	}
	return true
}

// Quotes group ordinary arguments only. Separators within quotes are literal.
func safeWords(s string) ([][]string, bool) {
	var parts [][]string
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
			started = true
		case c == ';' || c == '|' || c == '&':
			flush()
			if len(words) == 0 {
				return nil, false
			}
			parts = append(parts, words)
			words = nil
			if c == '&' {
				if i+1 >= len(r) || r[i+1] != '&' {
					return nil, false
				}
				i++
			} else if c == '|' && i+1 < len(r) && r[i+1] == '|' {
				i++
			}
		case unicode.IsSpace(c):
			flush()
		default:
			word.WriteRune(c)
			started = true
		}
	}
	flush()
	if quote != 0 || len(words) == 0 {
		return nil, false
	}
	return append(parts, words), true
}

func matchesAny(words, safe []string) bool {
	for _, s := range safe {
		want := strings.Fields(s)
		if len(want) > 0 && len(words) >= len(want) && strings.Join(words[:len(want)], " ") == strings.Join(want, " ") {
			return true
		}
	}
	return false
}

// Every option on these commands is opt-in: new write/execute options must not
// silently become safe when an installed utility changes. Arguments after --
// are data except for find/sed, which have their own expression languages.
func safeOptions(w []string) bool {
	switch w[0] {
	case "sed":
		return safeSed(w[1:])
	case "find":
		return allowedOptions(w[1:], "H L P name iname path ipath type maxdepth mindepth print print0 ls size mtime atime ctime newer empty readable writable executable user group perm links depth xdev mount prune a and o or not", "", false)
	case "rg":
		return allowedOptions(w[1:], "help version hidden files count count-matches line-number no-line-number ignore-case smart-case case-sensitive fixed-strings word-regexp line-regexp invert-match only-matching quiet files-with-matches files-without-match glob iglob type type-not max-count max-depth context before-context after-context color colors heading no-heading with-filename no-filename multiline multiline-dotall no-ignore no-ignore-vcs no-ignore-parent follow text json stats sort sortr null null-data regexp file", "nNiSwxvoclqHhUuaFefgABCmt", true)
	case "grep":
		return allowedOptions(w[1:], "help version extended-regexp fixed-strings basic-regexp perl-regexp regexp file ignore-case no-ignore-case word-regexp line-regexp invert-match count files-with-matches files-without-match line-number with-filename no-filename only-matching quiet silent recursive dereference-recursive binary-files text context before-context after-context max-count color colour include exclude exclude-dir null null-data", "EFGPefiwxvclLnHhoqsrRaIABCmzZ", true)
	case "git":
		if len(w) < 2 {
			return false
		}
		return allowedOptions(w[2:], "stat numstat shortstat summary name-only name-status patch no-patch raw check cached staged no-index color no-color word-diff word-diff-regex unified relative reverse quiet exit-code no-ext-diff no-textconv diff-filter find-renames find-copies full-index binary abbrev pretty format oneline max-count since until author all branches tags remotes graph decorate no-decorate porcelain short branch ignored untracked-files show-stash", "pUszMb", true)
	case "file":
		return allowedOptions(w[1:], "brief mime mime-type mime-encoding dereference no-dereference separator exclude", "bihLNkr0Fef", true)
	case "tree":
		return allowedOptions(w[1:], "dirsfirst filelimit charset noreport", "adfFlpugsiDnNQACrtvILP", true)
	}
	return true
}

func allowedOptions(args []string, long, short string, end bool) bool {
	allowed := " " + long + " "
	for _, arg := range args {
		if arg == "--" {
			return end
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		if strings.HasPrefix(arg, "--") {
			name, _, _ := strings.Cut(arg[2:], "=")
			if !strings.Contains(allowed, " "+name+" ") {
				return false
			}
		} else if short == "" {
			if !strings.Contains(allowed, " "+arg[1:]+" ") {
				return false
			}
		} else {
			for _, c := range arg[1:] {
				if !strings.ContainsRune(short, c) && !(c >= '0' && c <= '9') {
					return false
				}
			}
		}
	}
	return true
}

// Only simple printing scripts are automatically approved. sed's full script
// language (including e/w, scripts loaded with -f, and in-place edits) is gated.
var printScript = regexp.MustCompile(`^(?:[0-9]+(?:,[0-9]+)?|\$)?p$`)

func safeSed(args []string) bool {
	script := false
	for _, arg := range args {
		if !script {
			if arg == "-n" || arg == "-E" || arg == "-r" {
				continue
			}
			if !printScript.MatchString(arg) {
				return false
			}
			script = true
		} else if strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return script
}
