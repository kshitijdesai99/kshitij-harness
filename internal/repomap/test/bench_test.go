package test

import (
	"runtime"
	"testing"
	"time"

	"kh/internal/repomap"
)

func TestSpeedGoroot(t *testing.T) {
	s := time.Now()
	m := repomap.Build(runtime.GOROOT()+"/src", 20000)
	t.Logf("%d bytes in %v", len(m), time.Since(s))
}
