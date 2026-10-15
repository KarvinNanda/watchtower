# CLAUDE.md

Project instructions for Claude Code in the watchtower repo.

## Skills

- Always use the `superpowers` skills (for example `superpowers:brainstorming`, `superpowers:systematic-debugging`, `superpowers:test-driven-development`, `superpowers:verification-before-completion`). Check for a matching skill before starting any task.
- For any UI or frontend work (anything under `frontend/`, Vue components, styling, layout, accessibility, UX flows), always use the `ui-ux-pro-max` skills (for example `ui-ux-pro-max:ui-ux-pro-max`, `ui-ux-pro-max:ui-styling`, `ui-ux-pro-max:design-system`).
- When both apply, run the `superpowers` process skill first (for example brainstorming), then use `ui-ux-pro-max` for the implementation.

## Docs

- Design specs and implementation plans go under `docs/` (for example `docs/superpowers/specs/`, `docs/superpowers/plans/`). `docs/` is gitignored: never commit them, never put them in a branch or PR, and do not put them in Notion. This overrides the global "docs go to Notion" rule for this repo.
