package main

import (
	"fmt"
	"os"
	"strings"

	"kh/internal/backend"
	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/session"
	"kh/internal/terminal"
)

func listSessions(cfg config.Config) {
	entries := session.Recent()
	width := terminal.SessionWidth(os.Stdout)
	for _, entry := range entries {
		b, err := session.Load(entry.ID)
		text := "[preview unavailable]"
		var identity *session.Identity
		if err == nil {
			var saved session.Record
			saved, err = session.Decode(b)
			if err == nil {
				identity = saved.Identity
				c := cfg
				c.Provider = saved.Provider
				var p provider.Provider
				p, err = backend.New(c, "", nil, nil)
				if err == nil {
					err = p.Load(saved.State)
				}
				if err == nil {
					if responder, ok := p.(provider.LastResponder); ok {
						text = responder.LastResponse()
						if strings.TrimSpace(text) == "" {
							text = "[no assistant response yet]"
						}
					}
				}
			}
		}
		if err != nil {
			text = fmt.Sprintf("[unavailable: %v]", err)
		}
		fmt.Println(terminal.SessionRow(entry.SavedAt, entry.ID, identity.Label()+" "+text, width))
	}
}
