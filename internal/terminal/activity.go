package terminal

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/chzyer/readline"
)

// Activity animates only pane/tab titles, never the input or streamed text.
// The spinner indicates a responsive UI; a quiet timer indicates no new output.
type Activity struct {
	mu                      sync.Mutex
	out                     io.Writer
	name, phase             string
	liveDescendants         int
	parent                  bool
	enabled, active, closed bool
	started, updated        time.Time
	frame                   int
	done, stopped           chan struct{}
}

func NewActivity(out io.Writer, name string) *Activity {
	file, ok := out.(*os.File)
	enabled := ok && file != nil && os.Getenv("TERM") != "dumb" && readline.IsTerminal(int(file.Fd()))
	name = activityText(name)
	if name == "" {
		name = "kh"
	}
	a := &Activity{out: out, name: name, enabled: enabled, done: make(chan struct{}), stopped: make(chan struct{})}
	if enabled {
		go a.run()
	} else {
		close(a.stopped)
	}
	return a
}
func activityText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	r := []rune(s)
	if len(r) > 48 {
		r = r[:48]
	}
	return strings.TrimSpace(string(r))
}

// SetLiveDescendants updates the display name even while idle. The base name
// remains unchanged, so repeated updates and activity transitions cannot stack
// suffixes. It shares the renderer lock with spinner writes and Close.
func (a *Activity) SetLiveDescendants(count int, parent bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || (a.liveDescendants == count && a.parent == parent) {
		return
	}
	a.liveDescendants, a.parent = max(0, count), parent
	if !a.enabled {
		return
	}
	if a.active {
		a.drawLocked(time.Now())
	} else {
		fmt.Fprintf(a.out, "\x1b]2;%s\x07", a.displayNameLocked())
	}
}
func (a *Activity) displayNameLocked() string {
	if a.parent {
		return fmt.Sprintf("%s (%d)", a.name, a.liveDescendants)
	}
	return a.name
}

func (a *Activity) Start(phase string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || !a.enabled {
		return
	}
	a.active = true
	a.phase = activityText(phase)
	a.started = time.Now()
	a.updated = a.started
	a.frame = 0
	a.drawLocked(a.started)
}
func (a *Activity) Set(phase string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || !a.active {
		return
	}
	phase = activityText(phase)
	changed := a.phase != phase
	a.phase = phase
	a.updated = time.Now()
	if changed {
		a.drawLocked(a.updated)
	}
}
func (a *Activity) Stop() { a.mu.Lock(); defer a.mu.Unlock(); a.stopLocked() }
func (a *Activity) stopLocked() {
	if a.active {
		a.active = false
		fmt.Fprintf(a.out, "\x1b]2;%s\x07", a.displayNameLocked())
	}
}
func (a *Activity) Close() {
	a.mu.Lock()
	if !a.closed {
		a.stopLocked()
		a.closed = true
		close(a.done)
	}
	a.mu.Unlock()
	<-a.stopped
}
func (a *Activity) run() {
	defer close(a.stopped)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-a.done:
			return
		case now := <-tick.C:
			a.mu.Lock()
			if a.active && !a.closed {
				a.drawLocked(now)
			}
			a.mu.Unlock()
		}
	}
}
func (a *Activity) drawLocked(now time.Time) {
	const frames = "|/-\\"
	phase := a.phase
	if quiet := now.Sub(a.updated); quiet >= 20*time.Second {
		phase += fmt.Sprintf("; quiet %ds", int(quiet.Seconds()))
	}
	fmt.Fprintf(a.out, "\x1b]2;%s %c %s (%ds)\x07", a.displayNameLocked(), frames[a.frame%len(frames)], phase, max(0, int(now.Sub(a.started).Seconds())))
	a.frame++
}
