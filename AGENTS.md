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

Dashboard handoffs release terminal ownership and invoke shared CLI workflows.
Background effects return messages; UI state belongs to the model. Check late
results against target/generation, and let typing/paste own printable keys.

XDG preferences, metadata and state are separate on both OSes. Helper sidecars
are bound to command digests; native cron/Pueue works without lazycrontab. Upgrade
the resolved installed copy, not a PATH shadow, and preserve development builds
and package-owned installations outside their supported owner workflow.

Maintain only verified project information here. See docs/architecture.md and
docs/verification.md for boundaries and validation evidence.
