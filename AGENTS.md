# Project agent guidance

lazycrontab is a Go CLI/TUI for Linux/macOS user crontabs, explicit Supercronic
files and SSH fleet inspection. System sources are read-only. Native cron/files
remain authoritative; there is no scheduler, daemon or remote agent.

The root main package is installable with `go install .`; build using
`go build -o lazycrontab .`. The module requires Go 1.26.6+. Cobra and Charm v2
are pinned; do not mix Bubble Tea v1 examples into this code.

Checks:
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build -o /tmp/lazycrontab-dev .`
- `python3 scripts/pty_smoke.py /tmp/lazycrontab-dev`
- `python3 scripts/pty_managed.py /tmp/lazycrontab-dev`
- `python3 scripts/pty_raw_source.py /tmp/lazycrontab-dev`
- `python3 scripts/pty_review.py /tmp/lazycrontab-dev`
- `python3 scripts/pty_stable_form.py /tmp/lazycrontab-dev`
- `python3 scripts/pty_completion.py /tmp/lazycrontab-dev` (requires zsh)

Use isolated XDG roots and fixture backends. Never modify the developer's actual
crontab or submit real Pueue jobs as a smoke test. The PTY harness requires only
Python's standard library and verifies persistent terminal modes, masking BSD's
transient PENDIN input-retyping bit.

CLI/wizard/dashboard actions share internal/service. Internal/document preserves
unedited bytes. Internal/schedule adapts dialect-specific fields, environment
scope and percent semantics; retain compatibility fixtures when changing these.

Writes require a reviewed plan, fresh revision check, original-content backup and
read-back verification. Never automatically retry unknown remote writes. Native
crontab has no atomic compare-and-swap against external editors.

Dashboard job/source forms and host selection mount shared workflow models;
Playground is persistent and uses the same schedule editor as job forms.
Only external editor/SSH/tool handoffs release terminal ownership.
Background effects return messages; UI state belongs to the model. Check late
results against target/generation and model ownership. Nested help completion
must not close its containing workflow. Let typing/paste own printable keys.
Render and mouse hit regions share layout; overlays consume mouse events.
Add/Edit job drafts use an opt-in embedded popup, including their nested editors
and pickers. Stored inputs, rendered rows and focusable fields are distinct:
payload types share a slot, inactive script details reserve blank rows, and
runner-dependent rows stay visible but disabled. Up/down navigates fields;
left/right changes choices. Geometry and hit regions use the same row model.
The popup top is anchored to its maximum expanded structure; Advanced grows
downward and reveals its first new field. Capped forms expose their visible range.
Popup input never reaches the dashboard.
Mouse defaults on and is controlled only by config; no m or Alt+1/2/3 shortcuts
are built in. Reload propagates mouse settings to retained surfaces and clears
pending clicks. Standalone wizard layout is unchanged.
Job review projects the selected preset/runner without erasing other draft values;
inactive edits must not recompile an otherwise unchanged stored command. Preserve
legacy command script metadata and explicit Pueue redirects.
Review/apply/result forms are modal popups over the dashboard. Structured review
diffs use the built-in renderer; human receipts must preserve unknown/partial
outcomes, while JSON output keeps its structured data contract.
Execute owns the signal context; Bubble Tea uses WithoutSignalHandler.

Host discovery is static OpenSSH alias inventory or typed dev JSON, not shared
credential/config storage. Script presets and read-only checks never execute
user scripts or install dependencies. Generated arguments are percent-encoded
once for native cron. Preserve legacy recipes and stored commands on default Run.

Managed shell bodies are saved under the selected host's XDG data directory as
immutable content-addressed versions. Review/dry-run creates no target files.
Apply publishes and verifies the script under the source lock before installing
cron. Never overwrite or prune an old version: queued tasks/backups may use it.
Managed script editing goes through the job plan, not SaveScript. Multiline form
values must bypass textinput sanitization; preserve tabs/line endings via F4.

Raw source actions are v/V; sources show/edit-raw share the same snapshot/plan
services. sources edit still edits registration. Raw replacement keeps bytes and
does not re-render jobs or create IDs. New invalid syntax is rejected with line
diagnostics; unchanged unknown lines can be retained with warnings. Editor repair
keeps the original snapshot revision; failed writes never trigger automatic retry.

XDG preferences, metadata and state are separate on both OSes. Helper sidecars
are bound to command digests; native cron/Pueue works without lazycrontab. Upgrade
the resolved installed copy, not a PATH shadow, and preserve development builds
and package-owned installations outside their supported owner workflow.

Shell completion is read-only/offline. Host/source IDs come from config; job IDs
come from the bounded private completion cache populated by normal snapshots and
verified writes. Do not call SSH/crontab or create state from completion callbacks.
Cache keys include config and target identity, and omit command/script/env content.

Maintain only verified project information here. See docs/architecture.md and
docs/verification.md for boundaries and validation evidence.
