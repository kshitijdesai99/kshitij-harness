package terminal

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
)

const taskPrefix = "\x1b]kh-task;"

// TaskControl is a control-plane envelope, never human input or an approval.
// A generation fences late peer input from an earlier task in a warm process.
type TaskControl struct {
	Kind       string          `json:"kind"`
	Generation int64           `json:"generation"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Message    string          `json:"message,omitempty"`
}

func TaskFrame(control TaskControl) (string, error) {
	b, err := json.Marshal(control)
	if err != nil {
		return "", err
	}
	return taskPrefix + base64.RawStdEncoding.EncodeToString(b) + "\a", nil
}

func (c *Console) control(b []byte) bool {
	var control TaskControl
	if json.Unmarshal(b, &control) != nil {
		return true
	}
	if control.Kind == "assign" {
		select {
		case c.assignments <- append([]byte(nil), control.Payload...):
			return true
		case <-c.done:
			return false
		}
	}
	if control.Kind != "message" {
		return true
	}
	c.deliveryMu.Lock()
	defer c.deliveryMu.Unlock()
	if c.inputClosed {
		return false
	}
	if control.Generation != c.generation {
		return true
	}
	select {
	case c.lines <- control.Message:
		return true
	case <-c.done:
		return false
	}
}

// BeginTask drops already queued input from the previous assignment. The caller
// must have completed/cancelled its Runner before changing this boundary.
func (c *Console) BeginTask(generation int64) {
	// A producer may hold deliveryMu while waiting for a full Lines queue.
	// Drain while acquiring the boundary, otherwise resetting an idle worker can
	// deadlock behind precisely the stale input that must be discarded.
	for !c.deliveryMu.TryLock() {
		select {
		case <-c.lines:
		default:
			runtime.Gosched()
		}
	}
	defer c.deliveryMu.Unlock()
	c.generation = generation
	for {
		select {
		case _, ok := <-c.lines:
			if !ok {
				goto drained
			}
		default:
			goto drained
		}
	}
drained:
	c.mu.Lock()
	c.pending = nil
	c.taskEpoch.Add(1)
	// RuneBuffer.Set is internally synchronized. Clearing the active editor is
	// necessary too: an unfinished old 'y' must not approve a later task.
	if c.resetEditor != nil {
		c.resetEditor()
	}
	c.mu.Unlock()
}
