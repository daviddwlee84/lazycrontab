# Architecture and ownership

The root main package is installable. `internal/cli` constructs requests and presents results. `internal/ui` owns Bubble Tea state and reusable forms/reviews. Dashboard handoffs release the terminal and invoke those same CLI workflows, forwarding target and explicit config path.

`internal/document` retains source bytes and changes selected entries. `internal/schedule` validates dialects and computes descriptions/future matches without starting a scheduler. `internal/service` implements snapshots, plans, writes, backups, scripts, execution and forecasts. `internal/transport` invokes native tools locally or through a quoted OpenSSH boundary. `internal/config` implements XDG/typed TOML; `internal/upgrade` owns executable upgrades only.

Write path: snapshot → plan/diff → review → revision check → private original-content backup → native install/regular-file replacement → read-back receipt. A client-side source lock coordinates cooperating processes, not external editors or other workstations. Unknown outcomes are reconciled before retry.

Stable job IDs live in versioned comments. Optional local helper sidecars bind to target/source/job and command digest; emitted cron/Pueue commands execute without them. Reads do not assign durable IDs. Ordinary comments remain comments; only the dedicated disabled-entry marker identifies managed disabled jobs.

Effects return messages; the UI owns mutable state. Source reads use target/generation identities. Late discovery must not replace a review. Forms retain drafts across Back. Native editor/CLI handoffs avoid competing terminal readers. Selection follows job identity; source scopes retain filter/selection context. Mouse interactions use visible geometry, and Enter never approves a destructive review.

Registered targets load with bounded concurrency. Pueue discovery is optional/asynchronous. Snapshots remain in memory. Persistent files are written only for explicit registration, mutation, authentication and observed manual-run results. No database, scheduler, daemon, remote agent or LLM is required.
