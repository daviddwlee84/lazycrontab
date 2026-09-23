# lazycrontab

Understand and manage cron jobs from a Go CLI and terminal dashboard, locally or over SSH. Keep ordinary crontabs as the source of truth, with readable schedules, guided editing, a weekly forecast and optional Pueue submission.

Linux and macOS are supported. Remote hosts use their existing `crontab` and POSIX tools; no remote lazycrontab agent is required. Supercronic files are explicit additional sources. System crontabs are read-only.

## Build and run

Requires Go **1.26.6+**. These commands install this development checkout; no public tag or release is assumed.

```sh
go build -o lazycrontab .
./lazycrontab
go install .                 # GOBIN, or GOPATH/bin; ensure it is on PATH
go env GOBIN GOPATH
```

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

Hosts/sources, jobs and details stay visible together. Narrow terminals show the focused pane. Each host loads independently; failed refreshes retain their last snapshot with an error and observation time.

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
| `1` / `2` / `3` | Jobs / Week / Playground |
| `z` | Change this session's display timezone |
| `Ctrl+R` | Refresh |
| `a` / `s` | Add host / source |
| `A` | Native SSH authentication |
| `Q` / `R` / `C` | Configured lazypueue connection / source reload / config editor |
| `m` | Toggle mouse capture for native terminal selection |
| `?` / `Esc` / `q` | Help / Back / Quit |

Effective bindings drive both help and dispatch. Printable characters belong to the focused input, including `q`, `j` and `/`.

Forms use Tab/Shift+Tab, arrows for choices and `Ctrl+O` for advanced settings. `Ctrl+S` prepares a review; **`y` or another Ctrl+S applies, Enter does not approve**. Esc returns to the draft or cancels a standalone confirmation. Results remain visible until acknowledged.

Week starts on Monday. Arrows select a day/hour; Enter opens exact agenda rows; PgUp/PgDn pages; `[` / `]` changes weeks. UTC offsets distinguish repeated DST times. Counts and agenda are bounded and label truncation. These are forecast triggers, not execution history. Pueue jobs show enqueue times; execution may wait in the queue.

Playground shares the job builder and parser. Its advanced “After preview” choice can open a prefilled job draft. Preview alone never installs a job.

## CLI and automation

```sh
lazycrontab list --host all --source all --json
lazycrontab show JOB_ID --json
lazycrontab export > my-crontab.txt
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
lazycrontab backup list --json
lazycrontab backup restore BACKUP_ID --dry-run
lazycrontab schedule next '0 0 * * 7' --timezone Europe/London --count 10
lazycrontab schedule validate '*/2 * * * * * *' --dialect supercronic
lazycrontab overview --host all --source all --week 2026-09-21 --json
```

`all` aggregates reads; mutations require one host/source. List supplies job IDs. Unmanaged entries use revision-sensitive line references until their first managed edit adds a stable ID.

Bare add/edit, host/source forms and schedule build open wizards in a terminal. Partial business flags with missing required data fail unless `--interactive` is explicit. Global flags such as `--config` and `--host` alone do not suppress a wizard. Invalid flags/schedules fail before prompting. Pipes and JSON never prompt; use `--yes` after reviewing `--dry-run`.

Data uses stdout and diagnostics stderr. Exit codes: 0 success, 1 operation failure, 2 usage/config error, 130 cancellation. Direct CLI run failures preserve ordinary child exit codes. Successful Pueue submission means queued, not completed.

## SSH fleet and sources

```sh
lazycrontab hosts add
lazycrontab hosts add lab --ssh lab-server --timezone UTC --yes
lazycrontab hosts import --from ssh --dry-run
lazycrontab hosts import --from dev --yes
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

Static SSH import follows Include files and concrete Host aliases without connecting or evaluating Match commands. Dev import consumes `dev ssh list --json` when installed. Ordinary startup probes only registered hosts.

Native OpenSSH retains aliases, ProxyJump, agents and host-key checking. Background operations use BatchMode. Explicit authentication hands the terminal to SSH and honors configured ControlPath policies. Without one, an explicitly authenticated app-owned master expires after ten idle minutes and can be reused by CLI handoffs. No password is stored. Short private `/tmp/lct-UID-*` socket directories are referenced from XDG cache.

File paths must be absolute or quoted `~/...` paths expanded on the target. Symlinks can be read, but writing requires registering the resolved regular-file path. Source removal only removes registration. System sources need ordinary read permission; there is no automatic sudo escalation.

Supercronic saving and reload have separate results. For example, configure `reload = ["docker", "kill", "--signal=USR2", "worker-cron"]`, then explicitly invoke `sources reload`. The app never guesses a container/PID. A successful reload command does not independently prove runtime adoption.

## Scripts, execution and helpers

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

Output preserves existing behavior by default. `--output PATH` appends stdout/stderr together; `--stderr PATH` separates stderr. Parent directories must exist. Pueue uses its own capture by default. Review shows the generated shell command and required cron percent escaping.

Scripts use explicit paths, private editing copies, diffs, conflict checks, backups and permission-preserving replacement. Arbitrary command strings are not searched heuristically for a script filename.

System-cron manual runs use the displayed cron-like target environment. Supercronic manual runs inherit the selected host's noninteractive environment plus file assignments; an existing container process's environment/cwd cannot be recovered from the file. Neither mode claims exact daemon/PAM reproduction. Local cancellation terminates owned process groups; SSH interruption can leave the remote result unknown.

Only observed manual runs are recorded. Logs come from those records or explicit files, with bounded output. Native cron history stays unknown without an external observer.

## Configuration and storage

See [examples/config.toml](examples/config.toml). macOS and Linux both use XDG:

| Category | Default location | Contents |
| --- | --- | --- |
| Config | `~/.config/lazycrontab/config.toml` | Preferences, hosts, sources, key bindings |
| Data | `~/.local/share/lazycrontab/jobs/` | Optional helper recipes and script/log paths |
| State | `~/.local/state/lazycrontab/` | Backups and manual-run records |
| Cache | `~/.cache/lazycrontab/` | SSH references; snapshots otherwise remain in memory |

Absolute `XDG_*_HOME` variables override these roots; relative values are ignored. Reads and previews do not create directories. Explicit authentication, saves and observed manual runs create private state as needed. Files use 0600 and directories 0700.

Precedence is explicit flags → supported `LAZYCRONTAB_*` variables → TOML → defaults. Environment overrides: `LAZYCRONTAB_CONFIG`, `LAZYCRONTAB_HOST`, `LAZYCRONTAB_SOURCE`, `LAZYCRONTAB_LOCALE`. `config show --json` reports effective settings/path. Explicit missing or malformed config is an error; `config edit` remains available for repair.

Managed entries add a versioned metadata comment and ordinary cron line. Extra helper metadata is a digest-bound local sidecar. External command changes invalidate the recipe; `edit ID --command ...` explicitly replaces it. Without sidecars, native cron still executes and raw commands remain editable.

Unedited document bytes are retained. Writes back up original content, recheck the reviewed revision and verify the result. Native `crontab -` lacks compare-and-swap, leaving a short race with external editors. Unknown remote writes are never automatically replayed. Backups restore content; a previously absent source is restored as empty. This version never automatically prunes backups or run records.

See [cron compatibility](docs/compatibility.md) for dialect/time semantics.

## Completion and upgrades

```sh
lazycrontab completion zsh > _lazycrontab
lazycrontab completion bash > lazycrontab.bash
lazycrontab completion fish > lazycrontab.fish
lazycrontab upgrade --check --json
lazycrontab upgrade --yes
```

Load completion through your shell's normal directory (`fpath` on Zsh). Help/version/completion generation are offline.

Source-tag upgrades build an exact stable release with an installed compatible Go toolchain, verify module/version, then atomically replace the resolved running copy, including relocated installations. Verified Homebrew ownership delegates to the owning brew. Development/modified builds and unsupported managers are preserved. Upgrading never installs Go or upgrades cron/Pueue. Public install/upgrade verification requires a published tag; private file-backed Go proxy fixtures validate the implementation meanwhile.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/lazycrontab-dev .
python3 scripts/pty_smoke.py /tmp/lazycrontab-dev
```

Tests use isolated configuration and fake cron/SSH/Pueue tools, never real user jobs. The PTY harness needs only Python's standard library. CI defines Ubuntu/macOS tests and PTY runs plus Linux/macOS amd64/arm64 builds.

[Verification evidence](docs/verification.md) distinguishes executed checks from unverified live integrations. [Architecture](docs/architecture.md) describes ownership boundaries. MIT licensed; dependencies retain their licenses.
