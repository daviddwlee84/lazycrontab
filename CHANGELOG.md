# Changelog

## Unreleased

## v0.1.0 — 2026-09-24

Initial source release for Linux and macOS; requires Go 1.26.6 or newer.

### Features

- Manage local and SSH user crontabs and explicit Supercronic files through a shared CLI/TUI; inspect system sources read-only.
- Review exact changes before writing, preserve unrelated document bytes, create original-content backups, detect stale sources and verify saved content.
- Add/edit jobs in stable popups with consistent ↑↓ field navigation, ←→ choices, conditional input positions, mouse controls and readable results.
- Explain cron schedules, edit individual fields, generate supported schedules in Playground, transfer them into new jobs, and forecast a weekly grid or agenda.
- Run one-line commands or existing executable/Shell/Python/uv scripts; author managed multiline shell scripts with immutable target-side versions.
- Submit scheduled tasks through native Pueue 4.x, select existing groups, retain explicit redirects and distinguish queued work from completed execution.
- View raw sources with `v`; edit private copies with `V` or `sources edit-raw`, retaining invalid drafts for repair and using the normal review/backup path.
- Discover configured SSH aliases, optionally import dev host inventory, authenticate through native SSH and browse target-side paths without executing scripts.
- Configure XDG storage, themes, keys and mouse capture; access embedded concept help and offline shell completion for commands, options and target-specific IDs.
- Check for source updates and update the resolved installed binary; respect verified package-manager ownership and preserve development builds.

### Release boundaries

- Native cron/files remain authoritative. lazycrontab does not install a scheduler, daemon or remote agent.
- Pueue execution needs its CLI and daemon on the selected host. Forecasts describe configuration-derived trigger times, not execution history.
- Native crontab has no atomic compare-and-swap with external editors. Uncertain writes require reconciliation; they are never retried automatically.
- Public source installation requires a published tag; source update discovery additionally requires a stable GitHub Release. Binary archives and a Homebrew formula are not part of this initial source release.
