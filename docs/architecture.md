# Architecture and ownership

The root main package is installable. `internal/cli` constructs requests and presents results. `internal/ui` owns Bubble Tea state, shared forms/reviews, the schedule editor and host picker. A workflow factory mounts the same job/source forms in the dashboard or a standalone CLI. Playground is a persistent child model; Use transfers a schedule into an add draft. External editor, native SSH and third-party tool handoffs release terminal ownership.

Raw source viewing is an owned, read-only snapshot model with independent scroll
and search. Its edit event binds the viewed host/source, rather than a mutable
dashboard selection. Whole-source editing uses a local 0600 draft and the shared
PlanRawSource/Apply path. Syntax validation examines all proposed lines, including
lines the job parser cannot understand; unchanged unsupported lines may be retained
with warnings. Repair reopens the same draft against the original revision. No raw
edit automatically creates IDs, rewrites helper metadata or deletes script files.

`internal/document` retains source bytes and changes selected entries. `internal/schedule` validates dialects and computes descriptions/future matches without starting a scheduler. `internal/service` implements snapshots, plans, writes, backups, scripts, execution and forecasts. `internal/transport` invokes native tools locally or through a quoted OpenSSH boundary. `internal/config` implements XDG/typed TOML; `internal/upgrade` owns executable upgrades only.

Write path: snapshot → plan/diff → review → revision check → private original-content backup → native install/regular-file replacement → read-back receipt. A client-side source lock coordinates cooperating processes, not external editors or other workstations. Unknown outcomes are reconciled before retry.

The shared form renders review/apply/result as an opaque popup over the retained
dashboard, with one input owner and explicit acknowledgement before refresh.
Job drafts opt into the same popup presentation when embedded; their nested
editors and pickers use the frame's inner dimensions and translated input.
Drafts keep stored inputs separate from rendered rows and focusable fields.
Shared slots coalesce mutually exclusive inputs, reserved hidden rows render
blank, and disabled rows retain an explanation without input actions. Rendering,
mouse hit regions and scrolling share those rows. The popup's top edge is based
on its maximum expanded structure; Advanced grows downward and reveals its first
new field. Capped forms remain scrollable. Popups own input until completion or cancellation.
Standalone wizards keep their existing layout. Mouse capture follows the XDG
setting across config reloads, including retained editors; there is no session
toggle or built-in Alt view-switching binding.
Job planning projects only values applicable to the chosen preset/runner while
retaining other draft inputs. Unchanged effective execution preserves installed
commands and pinned runtime/Pueue paths; legacy command script metadata remains
an explicit exception to inactive script input filtering.
Review text and raw diffs are separate presentation fields; a built-in renderer
wraps and highlights changes without invoking a pager. Human save/run summaries
do not replace machine receipts or collapse partial/unknown outcomes into success.

Stable job IDs live in versioned comments. Optional local helper sidecars bind to target/source/job and command digest; emitted cron/Pueue commands execute without them. Reads do not assign durable IDs. Ordinary comments remain comments; only the dedicated disabled-entry marker identifies managed disabled jobs.

Effects return messages; the UI owns mutable state. Source reads use target/generation identities. Child completion and editor previews include model ownership, so old replies cannot close or update a new draft. Forms debounce/cancel discovery, preserve user edits when suggestions arrive, and cache previews outside View. Nested concept help has its own completion event. Dispatched writes are reconciled even when result/sidecar persistence fails; they are never blindly retried.

Selection follows job identity; source scopes and Playground retain their context. Pure layouts supply rendering and visible mouse rectangles; hovered panes own wheel events and overlays consume input. Printable characters belong to fields. CLI Execute owns the OS signal context; Bubble Tea programs disable their competing signal handlers. This preserves a single cancellation/terminal-cleanup path.

`internal/hostinventory` discovers conservative alias candidates from static OpenSSH files or dev's versioned JSON API. OpenSSH remains authoritative for connections; only selected aliases are registered. `internal/help` embeds the same offline concepts used by CLI and TUI. No dev private configuration or credentials are shared.

Optional ScriptTask recipes produce quoted native commands with explicit runtime, paths, working directory and literal per-job environment. Generated payloads are encoded once for native cron percent rules. Script discovery and preflight inspect bounded files/directories without executing user code. Default manual Run uses the stored command; explicit runner overrides may recompile the matching recipe.

Managed shell scripts attach an optional ManagedScriptPlan to the same source
plan. Target XDG discovery and version inspection are read-only. Apply checks
source/previous-script freshness under the source lock, backs up the source,
publishes a private immutable script using a no-clobber hard link, verifies it,
then checks freshness again before installing cron. Receipts report script and
cron outcomes separately. Published versions remain on failure and after
edit/remove because queued work and backups may still reference them. Editor
handoffs use private local drafts; they never edit a managed version in place.

Registered targets load with bounded concurrency. Pueue discovery is optional/asynchronous. Full snapshots remain in memory. Normal successful reads also save minimal job ID/name/status candidates in a private disposable XDG completion cache; it contains no commands, scripts or environment. Completion callbacks read config/cache only, never discover targets or authenticate. Config/transport/source identity and a 24-hour freshness bound prevent cross-target fallback. Writes invalidate old candidates before dispatch and repopulate after verified read-back. Cache failures never fail the underlying read/write. Other persistent files are written for explicit registration, mutation, authentication and observed manual-run results. No database, scheduler, daemon, remote agent or LLM is required.
