# kh

A lightweight coding harness with agent delegation and a database-backed memory architecture for continuous learning and relevant context retrieval.

You set the direction; the main agent manages the work, delegates implementation and inspection to native tmux agents or external coding tools such as Claude Code, and coordinates verification of their results. Workers execute scoped tasks so routine implementation and inspection stay off the main chat's plate.

kh provides chat, shell commands, file edits, shared memory, and agent coordination. The manager model is the working philosophy, not a guarantee of automatic delegation.

Start with the [feature index](features/README.md) to see what exists, when it began, and what followed. Each feature page owns its operating instructions, limitations, development history, and source/test evidence.

![kh terminal showing rendered Markdown responses](sample.png)

## Setup

Requires Go 1.24+ and Unix with `/bin/bash`. Install tmux for interactive chat; one-shot tasks do not require it.

From this checkout:

```bash
go build -o kh ./cmd/kh
./kh login codex                 # ChatGPT login; no API key
```

## Use

```bash
./kh                            # interactive chat
./kh "fix the failing tests"     # task, then stay in chat
./kh -p "fix the failing tests"  # headless task, then exit
./kh -r                         # resume this folder's latest session
./kh --sessions                 # list saved sessions
./kh -s SESSION_ID              # resume a specific session
```

Run `./kh --help` for CLI options or `/help` in chat. See [input](features/input.md), [steering](features/steering.md), [images](features/images.md), and [compaction](features/compaction.md) for chat controls; [saved sessions](features/saved-sessions.md) and [tmux chats](features/tmux-chats.md) explain how to continue work.

## Updates

Session startup rebuilds from local sources; already-running chats keep their current build. See [rebuild and updates](features/startup-rebuild.md) for requirements, controls, and restart instructions, and [dated release notes](release-notes/) for changes.

## Configuration and safety

Optionally create `~/.kh/config.json`, specifying only overrides:

```json
{ "effort": "high" }
```

CLI flags override configuration. See [project configuration](features/project-configuration.md) for `.agents`/`.claude` configuration, on-demand skills, local plugins, slash commands, and MCP capabilities (`.agent` remains an alias). Automatic approval is on by default; it does not remove write boundaries. Read [approval controls](features/write-approvals.md) and [sandbox limits](features/shell-sandbox.md) before running autonomous work.

## Memory and agents

![Memory overview: recall for answers and selective background learning](diagrams/memory-architecture.svg)

[Memory overview SVG](diagrams/memory-architecture.svg): answers use relevant recall; useful lessons are saved separately in the background. See [architecture](features/architecture.md) for system boundaries.

kh learns selectively from conversations and retrieves useful knowledge with provenance. Runtime notes and reusable procedures live in the database, separately from chat history and these docs. See [knowledge storage](features/knowledge-store.md), [learning](features/learning-hooks.md), [retrieval](features/keyword-recall.md), and [procedures](features/procedure-loading.md). Keyword search works without model downloads; hybrid retrieval is optional.

Ask kh to delegate independent work, then integrate and verify the results. See [native agents](features/native-agents.md) for coordination and [external coding CLIs](features/external-clis.md) for shell-based integration.

## Contributing

Coding agents should start at [AGENTS.md](AGENTS.md). Read [architecture](features/architecture.md), [development](features/development.md), and [documentation maintenance](features/CONTRIBUTING.md) before making changes.
