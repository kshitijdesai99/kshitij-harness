package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"kh/internal/memory"
)

// memoryCommand provides a small inspection and maintenance interface. Agents
// normally use the memory tool instead of shell commands (which may be sandboxed).
func memoryCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kh memory [list|search <query>|get <id>|remember <repo|global> <key> <kind> <title> <summary> <detail> [keywords]|forget <repo|global> <key>]")
	}
	s, err := memory.Open(memory.Path())
	if err != nil {
		return err
	}
	defer s.Close()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo := memory.RepoScope(cwd)
	ctx := context.Background()
	scope := func(v string) (string, error) {
		switch v {
		case "repo":
			return repo, nil
		case "global":
			return "global", nil
		}
		return "", fmt.Errorf("scope must be repo or global")
	}
	switch args[0] {
	case "list", "search":
		if args[0] == "list" && len(args) != 1 || args[0] == "search" && len(args) != 2 {
			return fmt.Errorf("usage: kh memory list | kh memory search <query>")
		}
		q := ""
		if len(args) == 2 {
			q = args[1]
		}
		entries, err := s.Find(ctx, repo, q, 30)
		if err != nil {
			return err
		}
		for _, e := range entries {
			fmt.Printf("[%d] %s (%s, %s): %s\n", e.ID, e.Title, e.Scope, e.Kind, e.Summary)
		}
		return nil
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: kh memory get <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}
		e, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		if e.Scope != repo && e.Scope != "global" {
			return fmt.Errorf("memory not in this repo")
		}
		fmt.Printf("[%d] %s (%s, %s): %s\n%s\n", e.ID, e.Title, e.Scope, e.Kind, e.Summary, e.Detail)
		return nil
	case "remember":
		if len(args) != 7 && len(args) != 8 {
			return fmt.Errorf("usage: kh memory remember <repo|global> <key> <kind> <title> <summary> <detail> [keywords]")
		}
		sc, err := scope(args[1])
		if err != nil {
			return err
		}
		e := memory.Entry{Scope: sc, Key: args[2], Kind: args[3], Title: args[4], Summary: args[5], Detail: args[6]}
		if len(args) == 8 {
			e.Keywords = args[7]
		}
		id, err := s.Put(ctx, e)
		if err != nil {
			return err
		}
		fmt.Printf("remembered [%d]\n", id)
		return nil
	case "forget":
		if len(args) != 3 {
			return fmt.Errorf("usage: kh memory forget <repo|global> <key>")
		}
		sc, err := scope(args[1])
		if err != nil {
			return err
		}
		ok, err := s.Forget(ctx, sc, args[2])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("memory not found")
		}
		fmt.Println("forgot", args[2])
		return nil
	default:
		return fmt.Errorf("unknown memory command %q", args[0])
	}
}
