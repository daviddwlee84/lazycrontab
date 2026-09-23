# Verification — 2026-09-24

Executed against isolated fixtures; no live user crontab or Pueue job was changed.

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
