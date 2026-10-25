# kh manual

Design and operating notes for kh’s implemented behavior. Start with the [project README](../README.md) for setup and everyday commands.

- [Feature index](../features/README.md): feature names, introduction dates, and subsequent developments, linking to individual feature pages. Start here when planning improvements; follow the [maintenance guide](../features/CONTRIBUTING.md) for changes.
- [Architecture](architecture.md): application boundaries, the turn loop, and provider-owned history.
- [Memory and procedures](memory.md): durable knowledge, provenance, retrieval, and inert procedural references.
- [Delegation and tools](delegation.md): the manager philosophy, native workers, and external coding tools.
- [Safety and sessions](safety-and-sessions.md): approvals, sandbox limits, steering, persistence, and updates.

These documents explain the system. Runtime knowledge and procedures belong in the memory database, not a filesystem-backed skills library. Source links identify the implementation; the CEO/manager model describes how we want agents to use it.
