package learn

// RunOrigin fences evidence collected across multiple messages in one run.
// Once peer coordination contributes, later user steering cannot make that
// run's tool outputs or final reply user-origin evidence again.
// The zero value represents a clean run.
type RunOrigin struct{ peer bool }

// Observe records each actual message before its pre-learning hook runs.
func (o *RunOrigin) Observe(message string) {
	o.peer = o.peer || containsPeerMessage(message)
}

// Eligible reports whether the run can supply post-learning evidence.
func (o RunOrigin) Eligible() bool { return !o.peer }

// Context omits peer-derived discussion, including after failed/cancelled runs.
// Keep the fence nonempty so callers cannot fall back to the provider's reply.
func (o RunOrigin) Context(recent string) string {
	if o.peer {
		return "[Peer-coordinated turn omitted from learning context.]"
	}
	return recent
}
