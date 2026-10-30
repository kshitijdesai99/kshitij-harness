package tools

import "sync/atomic"

// ApprovalPolicy holds the live command approval setting shared by session tools.
// Changing it affects future requests, not approvals already in progress.
type ApprovalPolicy struct {
	auto atomic.Bool
}

func NewApprovalPolicy(auto bool) *ApprovalPolicy {
	p := &ApprovalPolicy{}
	p.SetAuto(auto)
	return p
}

func (p *ApprovalPolicy) Auto() bool { return p.auto.Load() }

func (p *ApprovalPolicy) SetAuto(auto bool) { p.auto.Store(auto) }
