package main

import (
	"fmt"
	"strings"

	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/session"
)

func listSessions(cfg config.Config) {
	entries := session.Recent()
	entries = entries[:min(20, len(entries))]
	for _, entry := range entries {
		b, err := session.Load(entry.ID)
		var preview lastReply
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
					p.Replay(&preview)
				}
			}
		}
		text := preview.text.String()
		if err != nil {
			text = fmt.Sprintf("[unavailable: %v]", err)
		} else if strings.TrimSpace(text) == "" {
			text = "[no assistant response yet]"
		}
		// Flatten multiline replies and truncate by rune, preserving UTF-8.
		runes := []rune(strings.Join(strings.Fields(text), " "))
		if len(runes) > 70 {
			runes = append(runes[:67], []rune("...")...)
		}
		fmt.Printf("%s  %s  %s\n", entry.SavedAt.Format("2006-01-02 15:04"), entry.ID, string(runes))
	}
}

// Replay can deliver multiple text parts per response. Keep the most recent
// contiguous reply, ignoring tool output and notices. A trailing query or tool
// action must not erase the previous assistant response.
type lastReply struct {
	text    strings.Builder
	inReply bool
}

func (p *lastReply) Reply(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if !p.inReply {
		p.text.Reset()
	}
	p.text.WriteString(text)
	p.inReply = true
}
func (p *lastReply) Query(string)  { p.inReply = false }
func (p *lastReply) Action(string) { p.inReply = false }
func (p *lastReply) Notice(string) { p.inReply = false }
