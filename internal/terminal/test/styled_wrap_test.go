package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"kh/internal/terminal"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/reflow/wordwrap"
)

// A breakpoint is printable text too; omitting its cell width makes the
// paragraph wrapper overflow before Glamour's document wrapper runs.
func TestWordWrapCountsBreakpointCells(t *testing.T) {
	for _, c := range []struct {
		name, source, want string
		breakpoints        []rune
	}{
		{"hyphen", "abc-def x", "abc-def\nx", nil},
		{"repeated-hyphen", "abc--de x", "abc--de\nx", nil},
		{"styled-hyphen", "abc-\x1b[3mdef\x1b[0m x", "abc-def\nx", nil},
		{"wide-breakpoint", "abc界de x", "abc界de\nx", []rune{'界'}},
	} {
		t.Run(c.name, func(t *testing.T) {
			flow := wordwrap.NewWriter(8)
			if c.breakpoints != nil {
				flow.Breakpoints = c.breakpoints
			}
			if _, err := flow.Write([]byte(c.source)); err != nil {
				t.Fatal(err)
			}
			if err := flow.Close(); err != nil {
				t.Fatal(err)
			}
			if got := ansi.Strip(flow.String()); got != c.want {
				t.Fatalf("wrapped=%q, want %q", got, c.want)
			}
			for _, line := range strings.Split(flow.String(), "\n") {
				if width := ansi.StringWidth(line); width > 8 {
					t.Fatalf("line %q has width %d > 8", line, width)
				}
			}
		})
	}
}

// A real terminal is required: redirected renderers deliberately bypass Markdown.
func TestStyledWrapProcess(t *testing.T) {
	source := os.Getenv("KH_STYLED_WRAP_SOURCE")
	if source == "" {
		return
	}
	r := terminal.NewRenderer(os.Stdout, os.Stdout)
	for i := range []byte(source) {
		r.Reply(source[i : i+1])
	}
	r.FlushReply()
}

func TestStyledParagraphWrapping(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for PTY")
	}
	const chip = "For your chip-factory example, you could spend years improving cloud-error triage and become very good at it without learning much about making chips. You would be getting deeper into *your service*, not necessarily deeper into *their product*."
	for _, c := range []struct{ name, source, visible string }{
		{"chip", chip, strings.ReplaceAll(chip, "*", "")},
		{"plain-hyphens", strings.ReplaceAll(chip, "*", ""), strings.ReplaceAll(chip, "*", "")},
		{"bold", strings.ReplaceAll(chip, "*", "**"), strings.ReplaceAll(chip, "*", "")},
		{"mixed", "A cloud-error " + strings.Repeat("ordinary words ", 12) + "*italic words* and **bold words** remain one paragraph.", "A cloud-error " + strings.Repeat("ordinary words ", 12) + "italic words and bold words remain one paragraph."},
		{"unicode", "A chip-factory " + strings.Repeat("日本語 café αβγ ", 14) + "*your service* and **their product**.", "A chip-factory " + strings.Repeat("日本語 café αβγ ", 14) + "your service and their product."},
		{"links", "A cloud-error " + strings.Repeat("readable words ", 12) + "[Docs](https://example.com/docs) and *your service*.", "A cloud-error " + strings.Repeat("readable words ", 12) + "Docs https://example.com/docs and your service."},
		{"blocks", "# Heading\n\nFirst **paragraph**.\n\nSecond *paragraph*.\n\n1. one\n\n1. two\n\n[Docs][ref]\n\n[ref]: https://example.com/docs\n\n```text\n*literal*\n\ncode stays\n```\n\n| Name | Value |\n| --- | --- |\n| Ada | 42 |\n", "DOCUMENT_BLOCKS"},
	} {
		for _, width := range []int{80, 181, 182, 190, 200} {
			for _, noColor := range []string{"", "1"} {
				t.Run(fmt.Sprintf("%s/%d/no-color=%s", c.name, width, noColor), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, "python3", "-c", styledWrapPTY, os.Args[0], fmt.Sprint(width), noColor, c.source, c.visible)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("PTY: %v\n%s", err, out)
					}
				})
			}
		}
	}
}

// Model terminal cells, including delayed autowrap, instead of assuming that
// stripping ANSI and splitting LF reproduces what the user actually sees.
const styledWrapPTY = `import os,pty,subprocess,sys,fcntl,termios,struct,re,unicodedata
binary,width,no_color,source,expected=sys.argv[1:]
width=int(width)
m,s=pty.openpty()
fcntl.ioctl(s,termios.TIOCSWINSZ,struct.pack('HHHH',40,width,0,0))
p=subprocess.Popen([binary,'-test.run=^TestStyledWrapProcess$'],stdin=s,stdout=s,stderr=s,env=dict(os.environ,KH_STYLED_WRAP_SOURCE=source,KH_AUTO_REBUILD='0',TERM='xterm-256color',NO_COLOR=no_color))
os.close(s)
buf=b''
try:
 while True:
  try: chunk=os.read(m,65536)
  except OSError: break
  if not chunk: break
  buf+=chunk
 p.wait(timeout=2)
 assert p.returncode==0,(p.returncode,buf)
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(m)
raw=buf.decode()
# The testing runner's PASS line is not part of the rendered document.
raw=raw.rsplit('PASS',1)[0]
osc=r'\x1b\]8;;[^\x1b\x07]*(?:\x1b\\|\x07)'
plain=re.sub(r'\x1b\[[0-9;:]*m','',re.sub(osc,'',raw))
assert '\x1b' not in plain,repr(raw)
if no_color: assert '\x1b[' not in raw,repr(raw)
elif '*' in source:
 assert '\x1b[3m' in raw or '\x1b[1m' in raw,repr(raw)
if '[Docs]' in source: assert '\x1b]8;;https://example.com/docs\x1b\\' in raw,repr(raw)
def cells(c):
 if unicodedata.combining(c):return 0
 return 2 if unicodedata.east_asian_width(c) in ('W','F') else 1
def cellwidth(s):return sum(cells(c) for c in s)
rows=[{}];x=y=0
for c in plain:
 if c=='\r':x=0;continue
 if c=='\n':
  y+=1
  while len(rows)<=y:rows.append({})
  continue
 n=cells(c)
 if n==0:
  previous=x-1
  while previous>0 and rows[y].get(previous)=='':previous-=1
  if previous>=0:rows[y][previous]=rows[y].get(previous,'')+c
  continue
 if x+n>width:
  x=0;y+=1
  while len(rows)<=y:rows.append({})
 rows[y][x]=c
 for i in range(1,n):rows[y][x+i]=''
 x+=n
screen=[''.join(row.get(i,' ') for i in range(max(row,default=-1)+1)).rstrip() for row in rows]
while screen and not screen[-1]:screen.pop()
wire=[s.rstrip() for s in plain.replace('\r\n','\n').split('\n')]
while wire and not wire[-1]:wire.pop()
assert screen==wire,('terminal autowrapped emitted lines',screen,wire,repr(raw))
if expected=='DOCUMENT_BLOCKS':
 text='\n'.join(screen)
 for value in ['Heading','First paragraph.','Second paragraph.','1. one','2. two','Docs https://example.com/docs','*literal*','code stays','Ada','42','│']:
  assert value in text,(value,screen,repr(raw))
 assert 'First paragraph.\n\nSecond paragraph.' in text,('paragraph boundary lost',screen)
 literal=next(i for i,line in enumerate(screen) if '*literal*' in line)
 code=next(i for i,line in enumerate(screen) if 'code stays' in line)
 assert code>literal+1,('code blank line lost',screen)
else:
 assert screen and all(screen),('artificial blank line in paragraph',screen,repr(raw))
 assert ' '.join(' '.join(screen).split())==' '.join(expected.split()),('paragraph text changed',screen,expected,repr(raw))
 for a,b in zip(screen,screen[1:]):
  next_word=b.split()[0]
  assert cellwidth(a)+1+cellwidth(next_word)>width,('premature paragraph break',width,screen,repr(raw))
`
