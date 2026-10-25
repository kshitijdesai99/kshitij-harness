# Maintaining feature documentation

Keep the feature index easy to scan while allowing each capability's description and history to grow independently.

## New features

1. Create one descriptive, stable filename in this folder, such as `steering.md`. Document a user-recognizable capability, not every implementation change.
2. State its purpose and first implementation date with a Git commit reference. Do not substitute the documentation date. If the origin cannot be established, say so rather than guessing.
3. Describe current behavior: entry points, defaults or opt-in requirements, limitations, and links to relevant manuals, implementation, and representative tests.
4. Add a short chronological evolution table with dates, changes, and commits. Separate shipped changes from proposals.
5. Add one row to [the index](README.md): feature, introduction date, and a short “what followed” signpost.

## Changes to existing features

Update the feature page's current behavior and append meaningful milestones to its history. Update the index signpost only when it helps navigation; do not copy every fix into the index. Add a dated release note for user-visible changes and update operating instructions only where needed.

Keep history on its owning page. If a feature grows into independent capabilities, split them into linked pages and explain their relationship. For replacements or removals, retain a short retired page with the date and successor link rather than erasing the history. Check incoming links before renaming a page.

## Evidence and scope

- Verify historical dates/claims against Git, and current behavior against source and relevant tests.
- Distinguish linked test coverage from a fresh test run or manual verification; never imply all checks passed because a test file exists.
- Check local links, section anchors, and agreement between index dates and feature-page dates.
- Link to detailed manuals instead of copying them. Runtime memory remains in the database; these files are the human-readable feature catalog, not executable procedures or a second memory store.
- Keep unimplemented requests in problem/feature-request tracking, not among shipped capabilities.
