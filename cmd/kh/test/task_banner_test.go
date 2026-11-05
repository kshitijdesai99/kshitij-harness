package test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"kh/internal/ecosystem"
)

func TestAssignedTaskBannerStartup(t *testing.T) {
	binary, _ := newEcosystemProviderRequests(t, func(step int) *projectCLICall {
		if step%2 == 0 {
			return &projectCLICall{Name: "bash", Arguments: `{"command":"printf TASK-TOOL-PROGRESS"}`}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	process, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateRoot(ctx, ecosystem.RootOptions{Name: "banner-manager", MaxAgents: 5, Process: process})
	if err != nil {
		t.Fatal(err)
	}
	task := "first assignment line\n" + strings.Repeat("complete long task ", 100) + "LAST-TASK-LINE"
	for _, tc := range []struct {
		name               string
		child, interactive bool
	}{
		{"headless-worker", true, false}, {"interactive-worker", true, true},
		{"headless-root", false, false}, {"interactive-root", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := os.Environ()
			if tc.child {
				reservation, err := store.Reserve(ctx, root.ID, tc.name, "short registry summary only")
				if err != nil {
					t.Fatal(err)
				}
				if err := store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, process); err != nil {
					t.Fatal(err)
				}
				env = append(env, "KH_ECOSYSTEM_ID="+root.EcosystemID, "KH_ECOSYSTEM_AGENT_ID="+reservation.Agent.ID, "KH_ECOSYSTEM_TOKEN="+reservation.Token, "KH_PARENT="+root.Address)
			}
			mode := "-p"
			if tc.interactive {
				mode = "-i"
			}
			cmd := exec.CommandContext(ctx, binary, mode, task)
			cmd.Stdin = strings.NewReader("")
			cmd.Env = env
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("startup: %s: %v", output, err)
			}
			out := string(output)
			tool, reply := strings.Index(out, "TASK-TOOL-PROGRESS"), strings.Index(out, "PROJECT-CLI-DONE")
			if tool < 0 || reply <= tool {
				t.Fatalf("missing/incorrect tool and reply ordering: %s", out)
			}
			if strings.Contains(out, "\x1b[") {
				t.Fatalf("redirected output contains ANSI: %q", out)
			}
			if tc.child {
				banner := "ASSIGNED TASK — " + tc.name + "\n" + task + "\n\n"
				if !strings.HasPrefix(out, banner) || strings.Count(out, task) != 1 || strings.Count(out, "ASSIGNED TASK") != 1 {
					t.Fatalf("banner must appear first, complete and once: %s", out)
				}
				if strings.Contains(out, "> "+task) {
					t.Fatalf("duplicate query: %s", out)
				}
			} else {
				if strings.Contains(out, "ASSIGNED TASK") {
					t.Fatalf("root gained banner: %s", out)
				}
				if got := strings.Contains(out, "> "+task); got != tc.interactive {
					t.Fatalf("root query behavior changed: %s", out)
				}
			}
			t.Logf("%s: banner=%d tool=%d reply=%d task occurrences=%d", tc.name, strings.Index(out, "ASSIGNED TASK"), tool, reply, strings.Count(out, task))
		})
	}
}
