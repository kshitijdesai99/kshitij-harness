# Delegation and tools

How kh supports a coordinator that delivers outcomes without making the human manage every step.

## Human as CEO, main agent as manager

The working philosophy is to keep direction and important choices with the human. The main agent should plan, delegate, oversee evidence, resolve conflicts, and present the result. It may do integration or focused implementation itself; delegation is a way to reduce cognitive burden, not a rule against touching code.

This is guidance, not a deterministic scheduler or a promise that every task launches workers. A useful request is:

> Split this change into independent tasks, delegate implementation and review, integrate the results, and report what you verified. Ask me only for decisions you cannot make safely.

## Native tmux agents

The [agents tool](../internal/tools/agents.go), wired to [tmux operations](../cmd/kh/agents.go), supports `spawn`, `send`, `peek`, and `list`. Spawning returns immediately. Workers have addresses such as `kh:review`; they can send results to their parent and coordinate directly with peers.

Workers run in the same project directory and share files, not isolated worktrees. Assign independent files or coordinate edits explicitly. They inherit active model, effort, auto-approval, and sandbox policy, but not session-only directory grants. Successful one-shot workers close their panes; failures remain visible. Use `keep` when follow-up work needs an open worker.

Peer messages are attributed and shown in amber, distinct from human input. They are not human approval responses or durable user instructions. Workers can retrieve shared memory, but automatic learning belongs to user-facing chats.

## External coding tools

The [bash tool](../internal/tools/bash.go) can invoke installed coding CLIs such as Claude Code, subject to authentication, approvals, and the environment’s write policy. This is shell-tool integration, not a native Claude provider, MCP client, or managed external-agent protocol. The current native model adapter is Codex.

External tool availability and login are prerequisites, not bundled capabilities. The coordinator must inspect results and verify changes rather than treating a subprocess’s success message as proof of correctness. Long-running CLI work may need a background command and subsequent status checks.

## Integrate and verify

A practical coordination pattern:

1. Inspect the project and divide work by ownership or independent concern.
2. Give each worker a concrete outcome, scope, and verification requirement.
3. Collect findings and resolve overlapping changes before integration.
4. Run relevant checks, review the combined diff, and state any remaining uncertainty.

The harness provides tools and visibility; the agent remains responsible for choosing and checking this workflow. See [safety and sessions](safety-and-sessions.md) for boundaries shared by parent and workers.
