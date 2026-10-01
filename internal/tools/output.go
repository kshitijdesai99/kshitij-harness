package tools

import (
	"strings"
	"sync"
)

// output retains only the first ceil(cap/2) and last floor(cap/2) bytes.
// Writes never buffer the intervening output, including single enormous writes.
type output struct {
	mu         sync.Mutex
	head, tail []byte
	cap        int
	cut        bool
}

func newOutput(cap int) *output { return &output{cap: cap} }

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	h, t := o.cap-o.cap/2, o.cap/2
	if len(o.head) < h {
		take := min(h-len(o.head), len(p))
		o.head = append(o.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > t-len(o.tail) {
		o.cut = true
	}
	if t > 0 {
		if len(p) >= t {
			o.tail = append(o.tail[:0], p[len(p)-t:]...)
		} else {
			discard := max(0, len(o.tail)+len(p)-t)
			copy(o.tail, o.tail[discard:])
			o.tail = append(o.tail[:len(o.tail)-discard], p...)
		}
	}
	return n, nil
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var b strings.Builder
	b.Write(o.head)
	if o.cut {
		b.WriteString("\n...[cut]...\n")
	}
	b.Write(o.tail)
	return b.String()
}
