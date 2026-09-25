# lazycrontab

Understand and manage cron jobs from a Go CLI and terminal dashboard, locally or over SSH. Keep ordinary crontabs as the source of truth, with readable schedules, guided editing, a weekly forecast and optional Pueue submission.

Linux and macOS are supported. Remote hosts use their existing `crontab` and POSIX tools; no remote lazycrontab agent is required. Supercronic files are explicit additional sources. System crontabs are read-only.

## Install / 安裝

```sh
brew install daviddwlee84/tap/lazycrontab
lazycrontab --version
lazycrontab upgrade --check
```

**v0.1.2** adds macOS/Linux amd64/arm64 binary releases and the personal Homebrew
formula. Go is optional for binary installs; runtime backends remain separate.
See [installation, completion and owner-aware upgrades](docs/distribution.md).
[MIT license](LICENSE).

## Install and run

Requires Go **1.26.6+**. Source version **v0.1.1** uses versioned Go installation.
The matching tag must be published on GitHub before installing it:

```sh
go install github.com/daviddwlee84/lazycrontab@v0.1.1
lazycrontab --version
```

For subsequent source releases, `go install github.com/daviddwlee84/lazycrontab@latest`
selects the latest published release tag, not necessarily the latest main-branch
commit. Install locations follow `GOBIN`, or `GOPATH/bin`; ensure that directory
is on PATH (`go env GOBIN GOPATH`). Releases currently use Go source installation.

To build a local checkout instead:

```sh
go build -o lazycrontab .
./lazycrontab
go install .                 # GOBIN, or GOPATH/bin; ensure it is on PATH
go env GOBIN GOPATH
```

Checkout builds report `dev`; version-qualified `go install` reports its module
version. See [the changelog](CHANGELOG.md) for release contents and
[completion and upgrades](#completion-and-upgrades) for shell setup and updates.

No config file is needed for the local user's crontab. Launch never installs a scheduler or starts a daemon.

```sh
lazycrontab add
lazycrontab list
lazycrontab schedule explain '0 9 * * MON-FRI'
lazycrontab schedule build --when 'every weekday at 09:00'
lazycrontab playground
lazycrontab overview --timezone Asia/Taipei
```

## Dashboard and forms

The dashboard uses colored focus, selection and status indicators. Wide terminals show hosts/sources, jobs and details; medium widths show jobs/details, with hosts available through Tab; narrow terminals show the focused pane. Each host loads independently; failed refreshes retain their last snapshot with an error and observation time.

| Key | Action |
| --- | --- |
| Arrows / `j k` | Select |
| `Tab` / `Shift+Tab`, `h l` | Focus panes |
| `gg` / `G` | First / last job |
| `/` / `:` | Search jobs / available actions |
| `n` / `e` | Add / edit with the shared wizard |
| `x` / `d` | Review enable/disable / removal |
| `r` | Review a manual run, optionally through Pueue |
| `E` / `L` | Edit an explicit script / inspect logs |
| `v` / `V` | View / edit the complete raw source |
| `1` / `2` / `3` | Jobs / Week / Playground |
| `F1` | Concepts and practical help (outside the cron editor) |
| `z` | Change this session's display timezone |
| `Ctrl+R` | Refresh |
| `a` / `s` | Add host / source |
| `A` | Native SSH authentication |
| `Q` / `R` / `C` | Configured lazypueue connection / source reload / config editor |
| `?` / `Esc` / `q` | Help / Back / Quit |

Effective bindings drive both help and dispatch. Printable characters belong to the focused input, including `q`, `j` and `/`.

Forms use ↓/Tab for the next field and ↑/Shift+Tab for the previous field, skipping unavailable fields. On choice fields, ←→ changes the value; j/k moves between fields and h/l or Space changes the choice. Letters remain text in text inputs. `Ctrl+P`/Browse selects target paths and `Ctrl+O` opens advanced settings. Enter on Schedule opens the shared cron editor; F1 opens contextual concepts and returns to the same draft. Nested pickers, cron editors and multiline editors retain their own arrow-key behavior. `Ctrl+S` prepares a review; **`y` or another Ctrl+S applies, Enter does not approve**. Esc returns to the draft or cancels a standalone confirmation. Results remain visible until acknowledged. Job/source forms and host selection stay inside the dashboard; external editors and native SSH temporarily take terminal ownership.

Add/Edit job forms open in a popup with the dashboard still visible. Its top edge
stays fixed, with room below for Advanced to expand. Changing runner or task type
keeps the focused row in place. Command, existing script and managed content share
one input position; inapplicable runtime/project/argument rows reserve blank space.
Runner-related rows stay in place and show a muted reason when unavailable.
Fields scroll with focus; schedule editing, path browsing, help and managed-script
content stay in the same popup and return to the current draft. Advanced expands
downward to fit the available height and focuses the first newly revealed field.
When space is limited, a visible row range and Tab/arrows/wheel hints show how
to reach the remaining fields. Collapsing Advanced or switching types retains
entered values; only values applicable to the selected task are used in its plan.
The popup owns input until you finish or cancel it. Standalone CLI wizards retain
their full terminal layout.

Review and save results also use a centered popup.
Enable/disable first shows the job and status transition, followed by the exact
crontab diff. Added/removed lines and changed portions are highlighted; long lines
wrap and can be scrolled with arrows/j/k, PgUp/PgDn or the mouse wheel. This renderer
is built in and needs no external pager. The result names what was saved and its
backup; uncertain writes stay explicitly uncertain. Enter acknowledges the result
and returns to the same selection/filter. CLI `--json` retains structured receipts.

`v` opens the selected source's original document, including comments and environment
assignments. Use ↑↓/j/k or PgUp/PgDn to scroll, ←→/h/l for long lines, `/` to find,
`n`/`N` for matches, `c` to copy the original bytes to the terminal clipboard, and
Esc to return. The viewer is a read-only snapshot; reopen it to refresh. Its
display adds line numbers and expands tabs, while the source and clipboard stay
unchanged. In All, these actions target the selected job's source; with no selected
job, choose a host/source first. A specifically selected empty source also works.

`V` opens a private copy in `$VISUAL`/`$EDITOR`, even for SSH sources, then shows
the whole-source diff before Apply. Invalid input reports line numbers and lets
you reopen the same draft. Saving unchanged content does nothing. Comments,
formatting and job order are preserved exactly; raw editing does not add job IDs.
The normal revision check, original backup and read-back verification still apply.
Changing a command or metadata ID can invalidate its helper recipe. System and
configured read-only sources allow viewing only. Supercronic reload stays separate.

Week starts on Monday. Arrows select a day/hour; Enter opens exact agenda rows; PgUp/PgDn pages; `[` / `]` changes weeks. UTC offsets distinguish repeated DST times. Counts and agenda are bounded and label truncation. These are forecast triggers, not execution history. Pueue jobs show enqueue times; execution may wait in the queue.

Playground is a persistent tab, sharing the cron editor and parser with job forms. Each box represents one cron field: `*/5`, `1,15` and ranges fit inside a box. F1–F4 switch fields, presets, limited English phrases and macros. Errors explain the affected field; incomplete drafts remain editable. Paste a complete expression to populate matching boxes. Press **u / Use in new job** to open an add draft with the expression filled in; returning preserves the experiment. Copy and preview do not install a job. While editing, numbers are text: click a header tab or press Esc to leave the input before using a view shortcut. Alt+1/2/3 have no built-in bindings.

Tabs, fields, selectors, action buttons, week cells, agenda rows and help support the mouse, enabled by default. The wheel scrolls the hovered pane; overlays consume their own events. Configure `mouse = false` to disable capture; `m` has no built-in action. Theme follows `theme = "auto"`, `"dark"` or `"light"`; `NO_COLOR` retains text and shape indicators.

## CLI and automation

```sh
lazycrontab list --host all --source all --json
lazycrontab show JOB_ID --json
lazycrontab export > my-crontab.txt
lazycrontab sources show
lazycrontab --host lab sources edit-raw
lazycrontab sources edit-raw --file ./my-crontab.txt --dry-run
lazycrontab add --name backup --when 'daily at 03:00' \
  --command '/home/me/bin/backup' --remark 'Database backup' --dry-run
lazycrontab add --name backup --schedule '0 3 * * *' \
  --command '/home/me/bin/backup' --yes
lazycrontab edit JOB_ID --remark 'Updated reason' --yes
lazycrontab edit JOB_ID --interactive --name backup
lazycrontab disable JOB_ID --yes
lazycrontab enable JOB_ID --yes
lazycrontab remove JOB_ID --dry-run --json
lazycrontab run JOB_ID --yes --json
lazycrontab logs JOB_ID --lines 200
lazycrontab check JOB_ID --json
lazycrontab help execution-environment
lazycrontab backup list --json
lazycrontab backup restore BACKUP_ID --dry-run
lazycrontab schedule next '0 0 * * 7' --timezone Europe/London --count 10
lazycrontab schedule validate '*/2 * * * * * *' --dialect supercronic
lazycrontab overview --host all --source all --week 2026-09-21 --json
```

`all` aggregates reads; mutations require one host/source. List supplies job IDs. Unmanaged entries use revision-sensitive line references until their first managed edit adds a stable ID.

Bare add/edit, host/source forms and schedule build open wizards in a terminal. Partial business flags with missing required data fail unless `--interactive` is explicit. Global flags such as `--config` and `--host` alone do not suppress a wizard. Invalid flags/schedules fail before prompting. Pipes and JSON never prompt; use `--yes` after reviewing `--dry-run`.

`sources show` prints exact original bytes (or source metadata plus content with
`--json`). `sources edit-raw --file LOCAL_FILE` previews or installs a complete
replacement document; combine it with `--dry-run` or `--yes` for automation.
`sources edit ID` continues to edit the registration, not the source contents.

Data uses stdout and diagnostics stderr. Exit codes: 0 success, 1 operation failure, 2 usage/config error, 130 cancellation. Direct CLI run failures preserve ordinary child exit codes. Successful Pueue submission means queued, not completed.

Ordinary cancellation is quiet in the installed binary. When using `go run`, the Go launcher itself may still print `exit status 130`.

## SSH fleet and sources

```sh
lazycrontab hosts add
lazycrontab hosts add lab --ssh lab-server --timezone UTC --yes
lazycrontab hosts discover --from ssh --json
lazycrontab hosts import --from ssh
lazycrontab hosts import --from dev --alias lab-server --dry-run
lazycrontab hosts import --from dev --alias lab-server --yes
lazycrontab hosts test lab
lazycrontab --host lab
lazycrontab --host lab hosts authenticate
lazycrontab sources add worker --host lab --kind file \
  --path /srv/worker/crontab --dialect supercronic --yes
lazycrontab sources add system --kind system --dialect system \
  --path /etc/crontab --yes
lazycrontab sources add system-backup --kind system --dialect system \
  --path /etc/cron.d/backup --yes
lazycrontab --host lab --source worker list
```

Bare `hosts add` opens a searchable alias picker with nothing selected. Use Space or click a checkbox, then **Add selected** (`Ctrl+S`). Registrations are saved locally and each selected host's crontab is checked asynchronously. An unreachable host stays registered; the result offers **Retry** and native **Authenticate** for the selected host. Timezone and Pueue settings are optional advanced configuration, not initial setup questions. `hosts import` opens the same picker in a terminal.

Static discovery follows Include files and concrete Host aliases without connecting or evaluating Match commands. Conditional or unsupported declarations remain uncertain and can be entered explicitly through **Manual alias**. The optional dev source consumes the versioned `dev ssh list --json` API, including its active/selectable state. It never reads dev's private remote or credential files. Ordinary startup probes only registered hosts; your entire SSH config is not automatically added to the dashboard. In automation, `--alias` chooses specific entries; import with `--yes` and no `--alias` registers every new selectable candidate.

Native OpenSSH retains aliases, ProxyJump, agents and host-key checking. Background operations use BatchMode. Explicit authentication hands the terminal to SSH and honors configured ControlPath policies. Without one, an explicitly authenticated app-owned master expires after ten idle minutes and can be reused across CLI invocations. A fresh background read checks that the connection remains usable after the handoff. Passwords and MFA stay with native SSH; lazycrontab stores no password. Short private `/tmp/lct-UID-*` socket directories are referenced from XDG cache. See [SSH hosts and authentication](docs/ssh-hosts.md) for keys, agents, connection reuse and the optional dev integration.

File paths must be absolute or quoted `~/...` paths expanded on the target. Symlinks can be read, but writing requires registering the resolved regular-file path. Source removal only removes registration. System sources need ordinary read permission; there is no automatic sudo escalation.

Supercronic saving and reload have separate results. For example, configure `reload = ["docker", "kill", "--signal=USR2", "worker-cron"]`, then explicitly invoke `sources reload`. The app never guesses a container/PID. A successful reload command does not independently prove runtime adoption.

## Scripts, execution and helpers

For a one-line command such as `echo "hi"`, choose **Shell command (one line)**.
No script file is needed. **Existing shell script** instead expects a filename
such as `/srv/jobs/hello.sh`; entering `echo "hi"` there would mean a file with
that name. Review now shows the task type and original command before the exact
generated crontab diff.

```sh
lazycrontab add --name hello --when 'every minute' \
  --command 'echo "hi"' --runner pueue --group default --interactive
```

Every minute is `* * * * *` (or the Every minute preset); `@minutes` is not a
supported cron macro.

For multiple lines, choose **Managed shell script (write content)**. Enter opens
the content editor; Enter inside it inserts a newline, and Ctrl+S/Esc returns to
the draft. F4 opens `$VISUAL`/`$EDITOR`, including when exact tabs or line endings
need preserving. No filename is required. Apply saves a private script version
under the selected host's XDG data directory (`~/.local/share/lazycrontab/scripts/`
by default), then installs the reviewed cron entry. Editing creates another
version, leaving existing queued Pueue tasks and backups able to use their old one.

```sh
lazycrontab add --when 'every minute' --script-content-file ./commands.sh \
  --runner pueue --group default --dry-run
lazycrontab edit JOB_ID --script-content-file ./updated-commands.sh --interactive
lazycrontab script edit JOB_ID
```

The content file above is local input; over SSH the managed version is stored on
the remote account. Review/cancel never publishes it. Old versions are retained
after edits and removal; automatic pruning is not performed.

The add wizard offers executable/shebang, Shell, Python, uv project and uv standalone presets. Choose the target-side script, then browse detected runtimes and projects; working-directory defaults and read-only findings appear in the draft. Python virtualenvs use their interpreter directly, without activation. uv project flags are generated from your selection.

```sh
lazycrontab add --preset uv-project --script ./jobs/report.py \
  --project . --schedule '0 9 * * 1-5' --dry-run
lazycrontab add --preset python --runtime /srv/report/.venv/bin/python \
  --script /srv/report/daily.py --arg 'customer reports' \
  --env MODE=batch --schedule '0 3 * * *' --yes
lazycrontab check JOB_ID
lazycrontab help uv
```

The helper resolves relative paths against a visible target-side base and generates an explicit working directory. Cron does not inherit your interactive `.zshrc`: select the runtime and required literal environment values. Preflight inspects files and project markers; it never runs the script, imports its modules or installs dependencies. A normal uv job may prepare its environment when the scheduled command actually executes. See [script presets, paths and environment](docs/scripts.md).

```sh
lazycrontab edit JOB_ID --script /home/me/bin/backup \
  --output /home/me/log/backup.log --yes
lazycrontab script edit JOB_ID
lazycrontab script edit JOB_ID --path /home/me/bin/backup
lazycrontab run JOB_ID --runner pueue --group backup --yes
lazycrontab edit JOB_ID --runner pueue --group backup --yes
lazycrontab edit JOB_ID --runner direct --yes
lazycrontab doctor --host all --json
```

Pueue needs a 4.x CLI and reachable daemon on the job's host. Groups are selected from existing groups; the app does not create them or change parallelism. Unavailable Pueue choices are disabled with a reason. Scheduled wrappers use native Pueue and do not require lazycrontab on the target. Missing lazypueue only disables its separate handoff; set `lazypueue_connection` for a remote target.

Newly generated Pueue jobs pass an absolute working directory through
`--working-directory`, without repeating `cd` in the task. A plain command such as
`echo "hi"` stays that command in Pueue and uses Pueue's configured shell (normally
`sh -c`). An explicit crontab `SHELL`, per-job environment values, output redirects or cron `%` stdin
require a wrapper to retain their semantics; unresolved `~/` directories retain
target-side expansion. Existing stored commands are preserved when toggling or
running a job. Pueue owns the [task working directory and shell invocation](https://github.com/Nukesor/pueue/blob/v4.0.2/pueue/src/daemon/process_handler/spawn.rs).

Scripts use explicit paths, private editing copies, diffs, conflict checks, backups and permission-preserving replacement. Arbitrary command strings are not searched heuristically for a script filename.

System-cron manual runs use the displayed cron-like target environment. Supercronic manual runs inherit the selected host's noninteractive environment plus file assignments; an existing container process's environment/cwd cannot be recovered from the file. Neither mode claims exact daemon/PAM reproduction. Local cancellation terminates owned process groups; SSH interruption can leave the remote result unknown.

Only observed manual runs are recorded. Logs come from those records or explicit files, with bounded output. Native cron history stays unknown without an external observer.

## Output, logs and cron mail

Advanced **Task output**, or `--output-policy`, controls the task's streams:

| Policy | Direct job | Pueue task |
| --- | --- | --- |
| `inherit` (default) | Native cron receives output; Supercronic logs it | Pueue captures output |
| `files` | Append to selected files | Append to selected files instead of Pueue capture |
| `stderr-only` | Discard stdout; retain stderr | Pueue captures stderr only |
| `discard` | Discard both task streams | Discard both task streams; retain task status |

For `files`, `--output PATH` appends stdout and stderr together; `--stderr PATH`
separates stderr. Using only `--stderr` leaves stdout with its normal destination.
At least one path is required. Existing file flags without `--output-policy`
continue to select file output; clearing the last path through those CLI flags
returns to `inherit`. In the form, choose **Inherit** to stop redirecting. Paths
belong to the selected host; parent directories must exist. You manage rotation
and cleanup. `--log PATH` only selects a file to inspect.

The file fields stay in the same place when disabled by another policy. Changing
choices retains their draft values; only active settings affect the saved job.
`stderr-only` means a stream, not failures only: successful programs can write
warnings to stderr, and failures can be silent. Review shows the output choices
alongside the exact command and cron percent escaping.

Pueue task output is separate from the notices printed by `pueue add` when cron
submits a task. Advanced **Enqueue notices**, or `--enqueue-output`, has two values:
`quiet` discards only that add command's stdout; `inherit` leaves it untouched.
Both retain stderr and the exit status. New Pueue jobs and direct-to-Pueue changes
default to `quiet`, avoiding routine task-ID mail. Existing Pueue jobs keep their
stored behavior until a change is explicitly reviewed. Stderr warnings can still
produce mail, even after a successful enqueue.
[Pueue's add command](https://github.com/Nukesor/pueue/blob/v4.0.2/pueue/src/client/commands/add.rs)
reports submission separately from the task's eventual execution.

Manual Run can show the task ID using a generated job's still-valid saved recipe;
missing or changed recipes retain the exact stored command. A successful enqueue
means queued, not completed. Use `pueue log TASK_ID` or lazypueue to inspect the
task. Even `--output-policy discard` does not hide enqueue stderr.

```sh
lazycrontab add --when 'every minute' --command 'echo "hi"' \
  --runner pueue --dry-run
lazycrontab edit JOB_ID --enqueue-output quiet --dry-run
lazycrontab edit JOB_ID --output-policy stderr-only --dry-run
lazycrontab edit JOB_ID --output-policy files \
  --output /srv/logs/job.log --stderr /srv/logs/job.err --dry-run
lazycrontab help output-and-mail
```

Native cron can mail captured output. `MAILTO` unset normally uses the crontab
owner; `MAILTO=""` suppresses mail, and a nonempty value chooses a recipient.
Assignments apply to following jobs until replaced. A nonzero exit alone does
not guarantee mail. Delivery depends on the host's mail service; review reports
the setting without claiming that delivery works.
[crontab mail settings](https://man7.org/linux/man-pages/man5/crontab.5.html)

Use `V` or `sources edit-raw` to change `MAILTO` with a whole-source review.
Putting it at the top affects all following jobs. Per-job **Variables** such as
`--env 'MAILTO='` do not configure cron's own mail handling. A shell's “You have
new mail” notice can refer to local cron mail; it is separate from macOS Mail.
lazycrontab does not clear mailboxes or change shell/mail-service configuration.
Supercronic uses its daemon/container logs for output and status; changing task
streams does not remove its own scheduler logs.
[Supercronic logging](https://github.com/aptible/supercronic#why-supercronic)

## Configuration and storage

See [examples/config.toml](examples/config.toml). macOS and Linux both use XDG:

| Category | Default location | Contents |
| --- | --- | --- |
| Config | `~/.config/lazycrontab/config.toml` | Preferences, hosts, sources, key bindings |
| Data | `~/.local/share/lazycrontab/jobs/` | Optional helper recipes and script/log paths |
| State | `~/.local/state/lazycrontab/` | Backups and manual-run records |
| Cache | `~/.cache/lazycrontab/` | SSH references and minimal job-ID completion candidates |

Absolute `XDG_*_HOME` variables override these roots; relative values are ignored. Reads and previews do not create directories. Explicit authentication, saves and observed manual runs create private state as needed. Files use 0600 and directories 0700.

Mouse capture is controlled only through the top-level setting in
`$XDG_CONFIG_HOME/lazycrontab/config.toml` (default `~/.config/lazycrontab/config.toml`):

```toml
mouse = true # default; set false for native terminal text selection
```

Restart the TUI after an external config edit, or use its `C` config-editor action;
returning from that editor reloads the preference for the dashboard and retained
Playground. A saved `false` stays disabled. There is no session mouse-toggle key.

Precedence is explicit flags → supported `LAZYCRONTAB_*` variables → TOML → defaults. Environment overrides: `LAZYCRONTAB_CONFIG`, `LAZYCRONTAB_HOST`, `LAZYCRONTAB_SOURCE`, `LAZYCRONTAB_LOCALE`. `config show --json` reports effective settings/path. Explicit missing or malformed config is an error; `config edit` remains available for repair.

Managed entries add a versioned metadata comment and ordinary cron line. Extra helper metadata is a digest-bound local sidecar. External command changes invalidate the recipe; `edit ID --command ...` explicitly replaces it. Without sidecars, native cron still executes and raw commands remain editable.

Unedited document bytes are retained. Writes back up original content, recheck the reviewed revision and verify the result. Native `crontab -` lacks compare-and-swap, leaving a short race with external editors. Unknown remote writes are never automatically replayed. Backups restore content; a previously absent source is restored as empty. This version never automatically prunes backups or run records.

See [cron compatibility](docs/compatibility.md) for dialect/time semantics.

## Completion and upgrades

Completion includes command-specific arguments, not just command names and flags:

| Input before Tab | Candidates |
| --- | --- |
| `lazycrontab sources edit ` | Source IDs on the selected host, including `user` |
| `lazycrontab --host lab sources edit ` | Source IDs registered for `lab` |
| `lazycrontab hosts edit ` | Saved host IDs and `local` |
| `lazycrontab hosts authenticate ` | Saved SSH host IDs |
| `lazycrontab edit ` / `show ` / `run ` | Recently observed job IDs for the selected host/source |
| `lazycrontab backup restore ` | Existing local backup IDs |
| `lazycrontab add --runner ` / `--preset ` | Supported values |
| `lazycrontab add --output-policy ` / `--enqueue-output ` | Supported output and enqueue policies |

`sources edit ID` edits registration settings. `sources edit-raw` edits the
document selected through `--host`/`--source` and takes no positional ID.

For the current Zsh session, after installing the binary on PATH:

```zsh
autoload -Uz compinit
compinit                     # omit if your shell/framework already initialized it
source <(lazycrontab completion zsh)
```

For persistent Zsh setup, generate `_lazycrontab` into a user-owned completion
directory, put that directory in `fpath` **before** the shell/framework runs
`compinit`, and open a new shell:

```zsh
mkdir -p ~/.zfunc
lazycrontab completion zsh > ~/.zfunc/_lazycrontab
# In your shell setup, before compinit / your framework:
fpath=(~/.zfunc $fpath)
```

Do not regenerate the script on every shell startup. Existing native Zsh bridges
query the invoked binary for candidates, so updating the binary supplies the
new IDs; older Bash bridges should be regenerated for the dynamic V2 generator.
Completion generation does not modify your shell startup files automatically.

```sh
lazycrontab completion bash > lazycrontab.bash
lazycrontab completion fish > lazycrontab.fish
lazycrontab completion powershell > lazycrontab.ps1
lazycrontab upgrade --check --json
lazycrontab upgrade --yes
```

Bash uses its normal bash-completion setup; Fish and PowerShell use their usual
completion-loading mechanisms. Candidate lookup is offline: Tab never connects
to SSH, authenticates, starts a TUI, runs crontab or writes cache/config files.
Host/source IDs come from local configuration. Job IDs/names come from a private,
minimal completion cache populated by ordinary successful reads/writes; command
text, script contents and environment values are not cached. Run
`lazycrontab --host lab --source user list` explicitly to refresh remote candidates.
Entries expire after 24 hours; changed target identity, missing or malformed
settings yield no dynamic candidates rather than falling back to another host.
Mutating commands still validate the actual target and revision when executed.
Local file flags use ordinary filename completion; target-side paths do not
suggest unrelated files on your workstation.

Source-tag upgrades build an exact stable release with an installed compatible Go toolchain, verify module/version, then atomically replace the resolved running copy, including relocated installations. Verified Homebrew ownership delegates to the owning brew. Development/modified builds and unsupported managers are preserved. Upgrading never installs Go or upgrades cron/Pueue. Versioned source installation needs a published tag; `upgrade --check` also needs a stable GitHub Release because it reads the repository's latest-release endpoint. Local source-proxy verification is distinct from a successful public installation; see [verification evidence](docs/verification.md).

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/lazycrontab-dev .
python3 scripts/pty_smoke.py /tmp/lazycrontab-dev
python3 scripts/pty_managed.py /tmp/lazycrontab-dev
python3 scripts/pty_raw_source.py /tmp/lazycrontab-dev
python3 scripts/pty_review.py /tmp/lazycrontab-dev
python3 scripts/pty_stable_form.py /tmp/lazycrontab-dev
python3 scripts/pty_completion.py /tmp/lazycrontab-dev  # requires zsh
```

Tests use isolated configuration and fake cron/SSH/Pueue tools, never real user jobs. The PTY harness needs only Python's standard library. CI defines Ubuntu/macOS tests and PTY runs plus Linux/macOS amd64/arm64 builds.

[Verification evidence](docs/verification.md) distinguishes executed checks from unverified live integrations. [Architecture](docs/architecture.md) describes ownership boundaries. MIT licensed; dependencies retain their licenses.
