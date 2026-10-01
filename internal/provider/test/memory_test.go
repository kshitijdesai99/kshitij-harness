package test

import (
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/provider"
)

func TestMemoryNotSavedInSession(t *testing.T) {
	p := provider.NewCodex(config.Defaults, "", nil, nil)
	p.SetMemory("transient retrieved detail")
	saved, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "transient retrieved detail") {
		t.Fatal("memory leaked into saved session")
	}
	resumed := provider.NewCodex(config.Defaults, "", nil, nil)
	if err := resumed.Load(saved); err != nil {
		t.Fatal(err)
	}
	again, err := resumed.Save()
	if err != nil || strings.Contains(string(again), "transient retrieved detail") {
		t.Fatalf("memory persisted after resume: %v %s", err, again)
	}
}
