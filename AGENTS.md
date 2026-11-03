# Working on kh

Use this file as the entry point for repository work. Read the relevant guide instead of duplicating its instructions here.

## Reading order

1. [README](README.md): purpose, setup, and quick start.
2. [Feature index](features/README.md): find the owning page for current usage, limitations, evidence, and development history.
3. [Architecture](features/architecture.md): system boundaries and how the pieces connect.
4. [Development](features/development.md): implementation and test conventions before changing code.
5. [Documentation maintenance](features/CONTRIBUTING.md): ownership, history, and link rules before changing docs.

## Working rules

- Every manager (the main user-facing chat and promoted descendants) plans, assigns implementation/integration edits, routine tests, builds, installation and restart execution to workers, coordinates their evidence, and reports verified outcomes. On promotion, hand existing execution tasks to workers before continuing. Finished workers or full capacity do not transfer execution back to managers; retain orchestration and arrange worker execution without role-switching workarounds. Managers delegate substantial investigation and source/document exploration; once delegated, do not independently repeat broad exploration. Use concise worker conclusions and targeted follow-ups first. Managers may answer ordinary informational questions directly, do initial scoping, and make narrow reads for specific missing evidence, conflicts or verification. Assigned workers return conclusions, exact references, checks and uncertainty, not raw history or bulk source; they need not promote or delegate recursively.
- Keep changes scoped, verify behavior before claiming success, and report checks actually performed.
- Update the relevant canonical guide and feature history when behavior changes; link to existing explanations instead of copying them. Keep simple SVG architecture and flow diagrams in [`diagrams/`](diagrams/), following the [visual explanation guidance](features/CONTRIBUTING.md#visual-explanations).
- These files explain the repository. Runtime knowledge and procedures belong in the memory database, not Markdown-backed skills or executable documentation.
