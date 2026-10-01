package main

import (
	"fmt"
	"strings"

	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/session"
	"kh/internal/terminal"
)

func listSessions(cfg config.Config) {
	ids := session.List()
	ids = ids[max(0, len(ids)-20):]
	for _, id := range ids {
		b, err := session.Load(id)
		var buf strings.Builder
		if err == nil {
			var saved session.Record
			saved, err = session.Decode(b)
			if err == nil {
				c := cfg
				c.Provider = saved.Provider
				var p provider.Provider
				p, err = provider.New(c, "", nil, nil)
				if err == nil {
					err = p.Load(saved.State)
				}
				if err == nil {
					p.Replay(terminal.Renderer{Out: &buf, Err: &buf})
				}
			}
		}
		if err != nil {
			fmt.Fprintf(&buf, "[unavailable: %v]", err)
		}
		first, _, _ := strings.Cut(buf.String(), "\n")
		if len(first) > 70 {
			first = first[:70] + "..."
		}
		fmt.Printf("%s  %s\n", id, first)
	}
}
