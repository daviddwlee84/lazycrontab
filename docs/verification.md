# Verification — 2026-09-24

Executed against isolated fixtures; no live user crontab or Pueue job was changed.

## Task output and cron mail revision

Go 1.26.6 race tests, vet and builds passed on macOS arm64 and in a disposable
Linux arm64 container. All six PTY suites passed on both platforms. After the
interactive managed-script prefill fix, CLI race/vet checks and the managed-script
PTY were repeated on both. These checks ran locally/in a container, not on hosted
GitHub Actions. Linux and macOS amd64 cross-builds also passed.

Service fixtures verify inherited, file, stderr-only and discarded task streams,
append/merge/separate-file behavior, preserved exit status, comments and percent
stdin. Native percent input round-trips through a single physical crontab line,
including literal backslashes. Fake Pueue tests distinguish task output from
enqueue stdout/stderr, preserve manual task IDs through verified frozen commands,
and retain exact source commands with an honest unavailable-ID warning when the
submission metadata cannot be verified. Enqueue-only migration preserves existing
payloads, shells and executable paths without probing or submitting to Pueue.

CLI tests cover legacy path flags, policy conflicts, inactive draft projection,
MAILTO scope, no-op/metadata edits, independent log paths and read-only previews.
The managed-script regression checks that an interactive enqueue override loads
the saved body and permits further edits, while headless notification migration
does not require the script file. CLI/F1 expose the same output-and-mail topic.

Real terminal cells confirm fixed runner/policy rows, disabled keyboard/mouse
skipping, editable empty file paths, clear/refill, retained values after policy
changes, and contextual help returning to the same draft at 120×54, 80×24 and
40×12. Long managed-script reviews are inspected through actual page navigation.
Native zsh Tab inserts both policy enums; generated Bash completion is syntax
checked. Native Bash Tab, real mail delivery and live scheduler/queue execution
were not exercised. No mailbox, crontab MAILTO, user shell or mail service changed.

## v0.1.0 source-release preparation

The committed implementation at `bce52b0` passed an exact-source installation
check using Go 1.26.6 and a private file-backed Go module proxy. Installing
`github.com/daviddwlee84/lazycrontab@v0.1.0` recovered `v0.1.0` from Go build
metadata without linker injection. Version/help, all five embedded concept
topics, fixed-UTC schedule JSON and all four completion generators passed;
Bash/Zsh scripts passed syntax checks. Native backend marker calls remained zero.
A clean Git source archive also built successfully, with its ordinary checkout
build correctly reporting `dev`.

The inspected module ZIP contained 129 files: 554,539 compressed bytes and
1,638,002 uncompressed bytes. Existing tracked SpecStory files were included
(633,261 uncompressed bytes); no evidence files were untracked or rewritten.
These numbers describe the implementation snapshot before the release-document
commit. Final tag verification uses its committed source tree separately.

This was an offline release-contract check, not a public installation. At this
preparation step no Git remote was configured and the intended GitHub repository
was not reachable. Public fixed-tag/latest installation, hosted CI and the
GitHub latest-release upgrade check require repository/tag/release publication.

## Stable form rows and navigation revision

The full race suite, vet and source build passed on macOS arm64 and Linux arm64
(Go 1.26.6 in a disposable container). Final UI race checks and the stable-form,
review/draft/Advanced/mouse, general, managed-script and raw-source PTY harnesses
passed on both. `scripts/pty_stable_form.py` reads emitted terminal cells and waits
for actual focus/value updates, rather than assuming a fixed render delay. At
120×54, 80×24 and 40×12 it compares popup bounds and runner screen rows before and
after direct/Pueue changes without resizing the terminal to force a layout pass.

Coverage includes ↑↓ navigation versus ←→ choice changes, text j/k ownership,
shared payload positions, blank inactive script details, disabled row skipping
and mouse rejection, downward Advanced growth, preserved values, and clearing
then refilling an existing Pueue output before blur. Model tests cover row/input
identity, all-disabled forms, late picker/editor results and unrelated async
defaults during a latched edit. CLI tests prove invalid inactive values cannot
affect any preset, metadata-only edits retain pinned commands, legacy script
metadata and explicit redirects remain compatible, and managed versions survive
switching back to commands. Async discovery also ignores inactive draft values.

Same-viewport direct/Pueue VHS screenshots were visually compared at all three
sizes. The VT grid checks use simple fixture text; Unicode cell boundaries remain
covered by the model tests. No user scripts or real Pueue tasks were run.

## Advanced layout and mouse preference revision

UI/CLI/config race tests, vet and source build passed on macOS arm64 and Linux
arm64 (Go 1.26.6 in a disposable container). Review/draft/Advanced/mouse, general
and managed-script PTY harnesses passed on both. At 180×60, the first Advanced
activation grows the popup and reveals its first new field without a resize
event; at 40×12, the focused advanced field and visible-range indicator remain
usable. Collapse/reopen retains entered values. Model tests cover dynamic field
visibility, late defaults, nested-editor sizing and click geometry after reflow.

Real PTYs verify Alt digits no longer switch views, Playground still switches
after leaving input, mouse capture defaults on, explicit false disables capture,
and m neither changes mouse mode nor prevents a configured action binding.
Standalone CLI confirmations follow the same mouse preference. Config and UI
tests verify false survives serialization/reload, retained surfaces receive the
new setting, and pending clicks are cleared. Same-viewport basic/advanced and
narrow VHS images were visually inspected; terminal restoration passed.

## Add/Edit draft popup revision

UI/CLI race tests, vet and source build passed on macOS arm64 and Linux arm64
(Go 1.26.6 in a disposable container). The extended review/draft, general and
managed-script PTY harnesses passed on both. They verify the retained dashboard,
text ownership, Alt view switching with draft retention, review/Back/save,
cancellation without publishing script files, and one write per confirmation.

Draft coverage includes nested help, path picker, schedule and multiline editors,
all basic/advanced fields at 40×12, mouse focus/wheel/buttons and outside-click
containment. Standalone Playground → Add → cancel also retains its full surface
and expression. Model tests cover resize, nested input coordinates, stale mouse
presses and explicit dashboard-only popup activation. Wide and narrow VHS images
were visually inspected; terminal restoration checks passed. Tests use private
fixtures and do not execute scheduled scripts or submit real Pueue tasks.

## Review popup and Pueue command revision

The full race suite, vet and source build passed on macOS arm64 and Linux arm64
(Go 1.26.6 in a disposable container). Final UI race checks and the review PTY
also passed on both after the acknowledgement-footer fix. The general,
managed-script and raw-source PTY harnesses passed on both with the new popup
coordinates and human result text.

`scripts/pty_review.py` verifies the dashboard remains behind the popup after a
full repaint, Enter never submits a review, outside clicks/wheels do not reach
the dashboard, repeated confirmation writes once, resize preserves the target,
and result acknowledgement retains selection/filter. Cancellation leaves the
source unchanged; an external edit produces a readable conflict without retry.
Model tests also cover full scrollable errors, multiline acknowledgement status,
late messages, dispatched-write cancellation, Unicode wrapping/hit geometry and
`NO_COLOR`. VHS review/result/return and narrow screenshots were visually checked.

Pueue compiler tests capture native argv and execute harmless fixture payloads
to verify plain commands, working-directory metadata, quotes/percent handling,
explicit shell selection, per-job environment, stdin, redirects and script
arguments. Existing stored-command behavior remains covered. Receipt tests
preserve full machine result data and distinguish saved/failed/unknown outcomes.
No real Pueue daemon task was submitted; terminal checks use fixture backends.

## Shell completion revision

The full race suite, vet and source build passed on macOS arm64 and Linux arm64
(Go 1.26.6 in a disposable container). The final CLI, completion-cache and service
race checks also passed on both platforms after the last completion changes.
`scripts/pty_completion.py` exercised actual Zsh Tab insertion on both: selected
host/source IDs, cached job IDs, typed option values, prefix matching, missing or
malformed config, and suppression of unrelated filename suggestions. Backend
markers confirm that completion never invokes SSH, crontab, dev or Pueue; the
harness also verifies terminal restoration and generated Zsh/Bash syntax.

Unit tests cover cache freshness, target isolation, private minimal records,
read-only completion callbacks, snapshot/write invalidation, bounded backup
metadata reads and rejection of symlinks/FIFOs. Shell setup and fixtures remained
isolated; the developer's shell startup files were not changed. Native Bash Tab,
Fish and PowerShell completion were not exercised. Job candidates are advisory
cached observations; executing a command still reads and validates its target.

## Raw source revision

The full race suite, vet and source build passed on macOS arm64 and Linux arm64
(Go 1.26.6 in a disposable container). `scripts/pty_raw_source.py` passed on both:
exact local/SSH stdout and JSON content, comments/environment/CRLF/tab retention,
empty sources, read-only system sources, selection in All, both scroll axes,
search text ownership, modal mouse containment, resize, editor terminal handoff,
default-No review, cancellation, original backups and exact read-back. The
existing general and managed-script PTY harnesses also passed on macOS.

The new raw PTY cases reopen the same invalid private draft, verify its original
bad bytes remain, then repair/discard or repair/review/apply. A fixture concurrent
source change is rejected. Unit tests verify unsupported old lines may be retained
without introducing new malformed lines, duplicate metadata is rejected, raw edits
do not allocate IDs or rewrite sidecars, and uncertain writes do not retry. A
three-second validator fuzz smoke completed 89,352 executions without failure.

Actual remote hosts and cron/Supercronic daemons were not changed or exercised.
The editor validates the supported cron dialect, not arbitrary shell program
correctness; system `crontab` remains the final installer for native sources.

## Managed shell script revision

The complete race suite, vet, source build and both PTY harnesses passed on macOS
arm64 and on Linux arm64 with Go 1.26.6 in a disposable local container.
`scripts/pty_managed.py` verifies inline multiline editing, ordinary Enter versus
Done, draft/review cancellation without publication, 0600 target script files,
Pueue command generation without submission, nested F4 terminal handoff, exact
tabs from the external editor, immutable versions through job/script edits and
read-only CLI previews. Every terminal session checks mode restoration.

Service/CLI regressions cover local and simulated SSH XDG resolution, private
file creation without changing user data-root permissions, symlink refusal,
original-body freshness during editing, source changes during publication,
partial/unknown writes, existing-version corruption, stored-path retention after
XDG changes, and metadata-only edits that preserve pinned execution even if
SHELL or Pueue availability changes. Script bodies were never executed by these
checks. Managed scripts in actual remote hosts or Supercronic containers have
not been exercised; the selected target filesystem must be available at runtime.

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
