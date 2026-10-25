# Working on kh

Use this file as the entry point for repository work. Read the relevant guide instead of duplicating its instructions here.

## Reading order

1. [README](README.md): purpose, setup, and quick start.
2. [Feature index](features/README.md): find the owning page for current usage, limitations, evidence, and development history.
3. [Architecture](features/architecture.md): system boundaries and how the pieces connect.
4. [Development](features/development.md): implementation and test conventions before changing code.
5. [Documentation maintenance](features/CONTRIBUTING.md): ownership, history, and link rules before changing docs.

## Working rules

- The main user-facing chat acts as manager: plan, delegate implementation and inspection to workers, coordinate their evidence, and report verified outcomes. Assigned workers execute their scoped tasks; this rule does not require them to delegate recursively.
- Keep changes scoped, verify behavior before claiming success, and report checks actually performed.
- Update the relevant canonical guide and feature history when behavior changes; link to existing explanations instead of copying them. Keep simple SVG architecture and flow diagrams in [`diagrams/`](diagrams/), following the [visual explanation guidance](features/CONTRIBUTING.md#visual-explanations).
- These files explain the repository. Runtime knowledge and procedures belong in the memory database, not Markdown-backed skills or executable documentation.
