# Maintaining feature documentation

Keep one home for each capability's usage, limitations, evidence, and history. This folder is the canonical project reference; do not create a parallel manual.

## Ownership

| Location | Owns |
|---|---|
| Root `README.md` | Purpose, setup, quick start, links |
| Root `AGENTS.md` | Agent reading order and links to repository guidance |
| `features/README.md` | Feature names, introduction dates, short evolution signposts |
| Individual feature pages | Purpose, current usage/defaults/limits, evidence, dated history |
| `features/architecture.md`, `features/development.md` | Shared system map and development conventions |
| `release-notes/YYYY-MM-DD.md` | What changed on that date, not current reference instructions |

Runtime knowledge and procedures remain in the database; documentation is not a second memory store or executable skills library.

## New features

1. Create a descriptive, stable filename, such as `steering.md`. Document a user-recognizable capability, not every implementation change.
2. State its first implementation date and Git commit. Do not substitute the documentation date; say when the origin is uncertain.
3. Describe current use, defaults, requirements, and limitations. Link to shared policies on their owning feature page instead of copying them. Include implementation and representative test evidence.
4. Add a short chronological evolution table. Separate shipped changes from proposals.
5. Add one index row: feature, introduction date, and what followed. If nothing followed yet, say so.

## Changes, splits, and retirement

Update the owning feature's current behavior and meaningful history. Change the index signpost only when useful, and add a dated release note for user-visible changes. One change can affect several features, but each page should explain only its own part.

Split independently useful capabilities into linked pages when needed. Preserve a short retired page with the date and successor link for replacements/removals. Keep paths/headings stable where practical; update every incoming link before renaming or moving a section, including links from diagrams.

## Verification

Check source/history claims, local links/anchors, index/page date agreement, and the pending diff for duplication or unnecessary complexity. Distinguish representative test coverage from fresh passing tests or manual verification. Report checks actually performed. Proposed functionality belongs in problem/feature-request tracking, not among implemented capabilities.
