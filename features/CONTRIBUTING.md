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
| `diagrams/*.svg` | Canonical, editable architecture and flow diagrams linked from relevant guides |
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

## Visual explanations

Where practical, explain complex architecture, relationships, data flow, or lifecycle with a simple SVG in the dedicated [`diagrams/`](../diagrams/) folder. Embed it in the relevant guide with a short caption and a link to the source or detail view. Do not draw inline ASCII/text or Mermaid diagrams in Markdown. Keep command and code examples as text, and use short prose for caveats. Avoid turning paragraph-heavy boxes into a diagram in name only.

- Match the existing [memory overview](../diagrams/memory-architecture.svg) style: clear titles, rounded boxes, restrained colors, and explicit triangle arrowheads. Keep overviews to at most eight nodes and about 120 visible words; split complexity into separately linked detail views rather than shrinking text or adding dense boxes. The existing seven-node memory overview is the preserved baseline.
- Link or embed an existing diagram before creating another view of the same flow. Keep the SVG as the single canonical source; do not maintain PNG copies. Temporary raster previews are verification artifacts only.
- Label arrow direction and meaning, distinguish synchronous work from background jobs, and show important trust/failure boundaries.
- Update affected diagrams with behavior changes. Check them against the owning code and feature pages, not an earlier picture.
- Render the updated diagram and inspect the actual image at full view and readable detail. Source/XML validation alone cannot catch clipped text, crossed labels, or misleading arrows.

## Verification

Check source/history claims, local links/anchors, index/page date agreement, and the pending diff for duplication or unnecessary complexity. Distinguish representative test coverage from fresh passing tests or manual verification. Report checks actually performed. Proposed functionality belongs in problem/feature-request tracking, not among implemented capabilities.
