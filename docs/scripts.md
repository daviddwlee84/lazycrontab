# Schedule a command or script

Open `lazycrontab add` and select **What to run**. Choose **Shell command (one
line)** for `echo "hi"`, a pipeline or another command; no script file is needed.
Choose an existing-script preset to select a file, or **Managed shell script
(write content)** to write multiple lines and let lazycrontab save the file.
The dashboard and standalone add/edit commands use the same form.
Browse opens a target-side picker; selecting a directory explores it, and
selecting a file fills the draft. No job runs while you browse or preview.

| Preset | Intended use |
| --- | --- |
| `command` | A shell command such as `echo "hi"`, kept as written |
| `executable` | An executable script using its shebang |
| `shell` | A script with an explicit shell, such as `/bin/bash` |
| `python` | An existing Python script, run with a chosen interpreter or virtualenv |
| `uv-project` | A Python script using a selected project's dependencies |
| `uv-script` | A standalone Python script, including inline dependency metadata |
| `managed-shell` | Shell script content saved and versioned on the selected host |

After you select a readable script, the form suggests available runtimes and
nearby Python projects. Empty runtime, project and working-directory fields get
matching defaults; existing choices are kept. Browse lets you change them.
The form shows the resolved target path and checks next to the relevant fields.
Choose **Enabled: false** to save a draft job while fixing a missing file or
runtime. The final review shows the exact command, schedule and findings.

## Managed shell scripts

Select **Script content** to open the multiline editor. Enter inserts a newline;
Esc or Ctrl+S returns to the form with the draft intact. F4 opens your configured
external editor, including for content that the inline editor cannot preserve
exactly. Returning from either editor changes only the draft. Review shows the
script body, target path, execution choices and crontab changes before Apply.

The default interpreter is `/bin/sh`. The working directory starts at your CLI
invocation directory locally or the selected account's home over SSH. It does
not default to the directory where lazycrontab stores script versions. Select
another shell explicitly for Bash or Zsh syntax.

On the selected host, files live under
`$XDG_DATA_HOME/lazycrontab/scripts/<job-key-hash>/<content-sha256>.sh`, with
`~/.local/share` as the XDG data fallback on Linux and macOS. Over SSH, this is
the remote account's data directory, not your workstation's. Files and their
managed directories are private. Previewing, cancelling and read-only checks
create no script files and never execute the content.

Apply writes an immutable content version before installing the reviewed cron
command, then verifies the saved crontab. An edit creates a new path, so older
cron backups and already queued Pueue tasks can keep using their original
version. Old versions remain after changing or removing a job; there is no
automatic pruning. If the crontab write fails or its outcome is uncertain, the
new version may remain for recovery. Never edit a managed version file in place:
use the job form, `edit --script-content-file`, or `script edit JOB_ID` to
review a new version.

Managed scripts are ordinary shell scripts: cron and Pueue can execute them
without lazycrontab running. Stored content has a 1 MiB limit. This convenience
does not install dependencies, load shell startup files or infer which runtime
your commands require.

The storage path is on the selected host's filesystem. For a Supercronic process
inside a container, that path must also be accessible inside the container; the
tool does not infer mounts or copy scripts into containers.

## Paths and working directory

Script presets turn helper paths into absolute target paths before saving.
Relative input uses the chosen working directory, or the CLI invocation
directory locally and the selected account's home remotely. A remote path is
never resolved against your workstation. In structured script helpers, `~/`
uses the selected account's home, matching the browser and discovery. A crontab
`HOME=...` assignment still controls the eventual process environment, but does
not change those resolved helper paths. Raw commands keep native shell expansion.

For existing files, the default working directory is the script's parent, or the
selected project for `uv-project`. It is separate from choosing a Python interpreter or project.
For example, a script opening `data/report.csv` opens that file relative to its
working directory, regardless of where the script itself is stored.

Raw `command` jobs keep their shell behavior. Their existing helper paths still
require absolute paths or `~/`; lazycrontab does not guess which words in an
arbitrary command are filenames.

## Python and uv

Python virtualenvs work by selecting their interpreter directly. There is no
need to source an activation script.

The uv project picker looks for `pyproject.toml` in the script directory and
its ancestors. The generated command includes the chosen project explicitly.
Standalone mode explicitly skips surrounding projects. A script with inline
dependency metadata uses those declarations, even when it lives inside a
project; the form points this out if you choose the project preset.

Normal `uv run` behavior can synchronize environments, install dependencies or
download Python when the job actually runs. Inspection does not run uv or check
imports. If you want an already prepared environment with no uv synchronization,
choose its Python interpreter instead. See the [uv script guide](https://docs.astral.sh/uv/guides/scripts/)
and [CLI reference](https://docs.astral.sh/uv/reference/cli/).

## Environment

Cron does not run inside your interactive terminal. Native manual runs use the
displayed cron-like HOME, SHELL and PATH plus crontab assignments. Daemon/PAM
details can differ. Supercronic inherits its own process environment; inspecting
a crontab file cannot recover the environment of an existing container.

The helpers pin executable paths and working directories. Add **Variables**
for explicit per-job values if needed; these do not alter other jobs. Values are
literal: `$HOME` in a variable value is not silently expanded.

No `.zshrc`, login profile, virtualenv activation script or `.env` file is
automatically sourced. `.zshrc` is for interactive zsh; choosing zsh or a login
shell does not reproduce an interactive terminal. See the [zsh startup-file
documentation](https://zsh.sourceforge.io/Doc/Release/Files.html).

## CLI examples

Flags expose the same choices as the form:

```sh
# Every minute: a command needs no script file. Pueue captures its output.
lazycrontab add --schedule '* * * * *' --command 'echo "hi"' \
  --runner pueue --group default --dry-run

# Read a local file, preview a managed version on the selected host.
lazycrontab add --when 'every 5 minutes' --script-content-file ./draft.sh \
  --runner pueue --dry-run

# Content flags select managed-shell automatically; no script path to choose.
lazycrontab add --schedule '* * * * *' --script-content 'echo "hi"' --dry-run

# Read-only preview; no crontab installation or script execution.
lazycrontab add --when 'daily at 03:00' \
  --preset shell --script ./scripts/backup.sh --runtime /bin/bash \
  --arg '--quiet' --dry-run

lazycrontab add --when 'every weekday at 09:00' \
  --preset python --script /srv/report/report.py \
  --runtime /srv/report/.venv/bin/python --directory /srv/report \
  --env 'REPORT_MODE=daily' --disabled --yes

lazycrontab add --when 'every 15 minutes' \
  --preset uv-project --script /srv/report/report.py \
  --project /srv/report --runtime /home/me/.local/bin/uv --dry-run

lazycrontab check JOB_ID
lazycrontab check JOB_ID --json

lazycrontab edit JOB_ID --script-content-file ./revised.sh --dry-run
```

Every minute is `* * * * *`; `@minutes` is not a supported cron macro. The
English schedule builder also accepts `every minute`.

`--script-content-file` always reads a local file and places the reviewed content
on the selected target. It and `--script-content` cannot be combined with each
other, `--command` or `--script`. An explicit preset must be `managed-shell`.
When editing a managed job, omitting the content flags preserves its body and
version. Changing only the name, remark, schedule or enabled state also keeps
the installed command and pinned runtime/Pueue paths, even if the surrounding
shell environment has changed. `edit JOB_ID --command ...` switches to a command and detaches the
managed-script association while retaining existing script versions.

Repeat `--arg` for exact script arguments and `--env` for literal `NAME=value`
assignments. In the form, quotes group spaces in the Arguments and Variables
fields; no shell substitution occurs there.

`check` observes path readability, runtime executability, working directories,
output destinations and selected uv context. It does not execute user code,
import modules, source startup files or install dependencies. Findings include
unknown/unreachable states separately from observed failures. A successful
inspection is not proof that the script's external services or imports work.
Running a job is a separate reviewed action.

Existing `--command ... --script PATH` calls retain their behavior: `--script`
records an editable file path and does not replace the command. Choose a script
preset explicitly to generate a command; `edit ID --command ...` returns a
structured script job to raw command mode.
