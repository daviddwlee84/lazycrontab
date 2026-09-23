# Architecture and ownership

The root main package is installable. `internal/cli` constructs requests and presents results. `internal/ui` owns Bubble Tea state, shared forms/reviews, the schedule editor and host picker. A workflow factory mounts the same job/source forms in the dashboard or a standalone CLI. Playground is a persistent child model; Use transfers a schedule into an add draft. External editor, native SSH and third-party tool handoffs release terminal ownership.

`internal/document` retains source bytes and changes selected entries. `internal/schedule` validates dialects and computes descriptions/future matches without starting a scheduler. `internal/service` implements snapshots, plans, writes, backups, scripts, execution and forecasts. `internal/transport` invokes native tools locally or through a quoted OpenSSH boundary. `internal/config` implements XDG/typed TOML; `internal/upgrade` owns executable upgrades only.

Write path: snapshot → plan/diff → review → revision check → private original-content backup → native install/regular-file replacement → read-back receipt. A client-side source lock coordinates cooperating processes, not external editors or other workstations. Unknown outcomes are reconciled before retry.

Stable job IDs live in versioned comments. Optional local helper sidecars bind to target/source/job and command digest; emitted cron/Pueue commands execute without them. Reads do not assign durable IDs. Ordinary comments remain comments; only the dedicated disabled-entry marker identifies managed disabled jobs.

Effects return messages; the UI owns mutable state. Source reads use target/generation identities. Child completion and editor previews include model ownership, so old replies cannot close or update a new draft. Forms debounce/cancel discovery, preserve user edits when suggestions arrive, and cache previews outside View. Nested concept help has its own completion event. Dispatched writes are reconciled even when result/sidecar persistence fails; they are never blindly retried.

Selection follows job identity; source scopes and Playground retain their context. Pure layouts supply rendering and visible mouse rectangles; hovered panes own wheel events and overlays consume input. Printable characters belong to fields. CLI Execute owns the OS signal context; Bubble Tea programs disable their competing signal handlers. This preserves a single cancellation/terminal-cleanup path.

`internal/hostinventory` discovers conservative alias candidates from static OpenSSH files or dev's versioned JSON API. OpenSSH remains authoritative for connections; only selected aliases are registered. `internal/help` embeds the same offline concepts used by CLI and TUI. No dev private configuration or credentials are shared.

Optional ScriptTask recipes produce quoted native commands with explicit runtime, paths, working directory and literal per-job environment. Generated payloads are encoded once for native cron percent rules. Script discovery and preflight inspect bounded files/directories without executing user code. Default manual Run uses the stored command; explicit runner overrides may recompile the matching recipe.

Registered targets load with bounded concurrency. Pueue discovery is optional/asynchronous. Snapshots remain in memory. Persistent files are written only for explicit registration, mutation, authentication and observed manual-run results. No database, scheduler, daemon, remote agent or LLM is required.
