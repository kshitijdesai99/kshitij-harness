package test

import (
	"sync"
	"testing"

	"kh/internal/tools"
)

func TestApprovalPolicy(t *testing.T) {
	for _, initial := range []bool{false, true} {
		p := tools.NewApprovalPolicy(initial)
		if p.Auto() != initial {
			t.Fatal("initial auto setting lost")
		}
		p.SetAuto(!initial)
		if p.Auto() == initial {
			t.Fatal("auto setting did not change")
		}
	}
	var zero tools.ApprovalPolicy
	if zero.Auto() {
		t.Fatal("zero policy enables auto")
	}
}

func TestApprovalPolicyConcurrent(t *testing.T) {
	p := tools.NewApprovalPolicy(false)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				p.SetAuto(i%2 == 0)
				_ = p.Auto()
			}
		}()
	}
	wg.Wait()
	p.SetAuto(true)
	if !p.Auto() {
		t.Fatal("final auto setting lost")
	}
}
