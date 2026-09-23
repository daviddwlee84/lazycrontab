# Verification — 2026-09-24

Executed against isolated fixtures; no live user crontab or Pueue job was changed.

## UI/UX revision

The revised code passed the complete race suite and vet on macOS arm64, and
the complete race suite, vet, source build and expanded PTY harness on Linux
arm64 using the declared Go 1.26.6 toolchain in a disposable local container.
The macOS PTY run also passed. The Linux module proxy was a read-only local
download cache; the repository mount was read-only and copied into the container.

The expanded harness verifies the reported `3 → 1 → 3` navigation problem,
numeric field input with Alt view switching, retained Playground → Add → Back
drafts, and that adding never replaces the currently selected job. It also
exercises clickable tabs and form controls, target-side script browsing,
script preset preflight, nested F1 help returning to the same draft, modal wheel
containment, SSH alias selection, external editor/authentication handoffs and
terminal restoration. Fixture markers prove script preflight never executes the
script and that the harness does not submit Pueue jobs.

Separate real PTYs covered uv project/runtime/cwd suggestions and host selection
through offline save, native authentication, single-host retry and return.
Six consecutive fleet SIGTERM checks returned 130 and restored terminal modes
after removing competing signal handlers. Model tests cover cancelled factories,
late child completion, partial-apply reconciliation, discovery suggestions that
must not overwrite user input, relative-path rebasing and preview ownership.

VHS screenshots were visually inspected at 128×33 and 40×12, in light and dark
themes. Cron field headings, error hints and Copy/Use actions remain visible;
narrow detail scrolling exposes full script paths. These snapshots complement
the behavioral PTY checks and are not a guarantee for every font or terminal.

## Earlier baseline verification

| Check | Evidence |
| --- | --- |
| macOS arm64, Go 1.27.0 | Unit/integration tests, race detector, vet and source build passed |
| Declared minimum Go 1.26.6, macOS arm64 | Full Go test suite passed |
| Linux arm64, Go 1.26.6 | Full race suite, vet, build and PTY harness passed in a disposable local Docker container using `golang:1.26.6-bookworm` |
| Cross-builds | Linux/macOS × amd64/arm64 built with CGO disabled |
| Parser fuzz smoke | 173,575 executions in a bounded three-second fuzz window; no failure |
| Document fuzz smoke | 112,289 executions in a bounded three-second fuzz window; no failure |
| Source upgrade | A private file-backed Go proxy built genuine v1.0.0/v1.0.1 module fixtures; the relocated installed executable updated while a different GOBIN/PATH copy remained untouched |
| Homebrew upgrade | Fake owning manager verifies receipt/Cellar routing, exact formula delegation, changed-file rejection and truthful no-op version reporting |
| Installation/completion | Isolated `go install .`, installed version/JSON/offline schedule/development upgrade check, and Bash/Zsh completion syntax passed |

The PTY harness drives actual terminal bytes through the compiled executable and fake native backends. It exercises text ownership, resize at 120×32 / 80×24 / 40×12, guided creation, review/Back/apply, Enter not approving deletion, cancellation, SGR mouse input, readable child-command errors, script editor return and mode preservation, weekly grid/agenda, playground/help, bare wizard entry with global target flags, native SSH authentication and SIGTERM exit. Echo, canonical mode, flow control and other persistent terminal attributes are compared before/after. BSD's transient PENDIN input-retyping bit is excluded from that comparison. Model tests separately check Unicode cell widths, tiny sizes, key conflicts, selected identity, stale replies, palette filtering and mouse geometry.

Backend tests cover original-byte retention, meaningful command whitespace, duplicate IDs, system-source read-only behavior, source conflicts, unknown write outcomes without retries, original backups, script permission preservation and symlink refusal. Schedule fixtures cover Sunday 7, named fields, DOM/DOW OR and leading wildcard semantics, impossible dates, leap/DST cases, Supercronic field ordering and finite-language representability. Helpers exercise quotes, dollar substitutions, percent stdin handling, redirects and native Pueue argv without executing the submitted payload. SSH tests distinguish app-owned fallback masters from user-configured sharing policies.

Limits: actual SSH servers, live cron daemons, Supercronic containers/reload signals and real Pueue submissions were not exercised. Public source tags and hosted CI have not been published/run. Forecasts are configuration-derived and cannot prove execution or daemon-specific DST catch-up. Native crontab provides no external-editor compare-and-swap. PTY assertions are behavioral checks, not a guarantee for every terminal/font combination. Fuzz results are short smoke coverage, not exhaustive verification.
