# Changelog

## Unreleased


## v0.1.2 - 2026-09-25

- Publish macOS/Linux amd64/arm64 binary archives, checksums and a filtered source archive.
- Add the personal Homebrew formula channel with generated Bash/Zsh completions.
- Provide `lazycrontab upgrade` and read-only `--check` through verified Homebrew ownership; preserve standalone/local copies and document their external update paths.
- Verify source/module packaging independently; retain embedded resources and development history outside release payloads.
- Distribute the application under MIT.

## v0.1.1 — 2026-09-24

- Choose per-job task output with `--output-policy` or Advanced: inherit the
  runner's handling, append to files, keep stderr only, or discard both streams.
  Output file fields keep their place and retain inactive draft values.
- Default new Pueue jobs to quiet enqueue notices, retaining enqueue stderr and
  manual Run task-ID feedback. Existing Pueue jobs keep their stored behavior
  unless a notice-policy change is explicitly reviewed.
- Explain task output, enqueue notices and applicable cron `MAILTO` settings in
  review and shared offline `output-and-mail` help. Mailbox and host mail-service
  configuration remain under user control.
- Keep native cron percent-separated stdin in a single physical crontab line,
  preserving literal backslashes, percent characters and trailing comments.
- Preserve managed-script content when opening an interactive edit with enqueue
  settings, and retain pinned commands when only log or notice settings change.
- Keep manual Run on the exact native command when helper metadata cannot be
  verified, reporting unavailable Pueue task IDs instead of inferring them.
- Make terminal acceptance checks observe completed zsh initialization and actual
  screen cells; run Linux and macOS CI tests independently.

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
