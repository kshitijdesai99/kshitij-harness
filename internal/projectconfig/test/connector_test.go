package projectconfig_test

import (
	"reflect"
	"testing"

	"kh/internal/projectconfig"
)

func TestConnectorCommandReserved(t *testing.T) {
	c := &projectconfig.Config{Commands: map[string]string{"connector": "shadow", "other": "other"}}
	for _, line := range []string{"/connector", "/connector ignored"} {
		if c.HasCommand(line) || c.Expand(line) != line {
			t.Fatalf("native command shadowed: %s", line)
		}
	}
	if !reflect.DeepEqual(c.CommandNames(), []string{"/other"}) {
		t.Fatal(c.CommandNames())
	}
}
