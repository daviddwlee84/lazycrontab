# Schedule a script

Open `lazycrontab add`, select **What to run**, then choose the script on the
selected host. The dashboard and standalone add/edit commands use the same form.
Browse opens a target-side picker; selecting a directory explores it, and
selecting a file fills the draft. No job runs while you browse or preview.

| Preset | Intended use |
| --- | --- |
| `command` | An existing shell command, kept as written |
| `executable` | An executable script using its shebang |
| `shell` | A script with an explicit shell, such as `/bin/bash` |
| `python` | An existing Python executable, including a project's `.venv/bin/python` |
| `uv-project` | A Python script using a selected project's dependencies |
| `uv-script` | A standalone Python script, including inline dependency metadata |

After you select a readable script, the form suggests available runtimes and
nearby Python projects. Empty runtime, project and working-directory fields get
matching defaults; existing choices are kept. Browse lets you change them.
The form shows the resolved target path and checks next to the relevant fields.
Choose **Enabled: false** to save a draft job while fixing a missing file or
runtime. The final review shows the exact command, schedule and findings.

## Paths and working directory

Script presets turn helper paths into absolute target paths before saving.
Relative input uses the chosen working directory, or the CLI invocation
directory locally and the selected account's home remotely. A remote path is
never resolved against your workstation. In structured script helpers, `~/`
uses the selected account's home, matching the browser and discovery. A crontab
`HOME=...` assignment still controls the eventual process environment, but does
not change those resolved helper paths. Raw commands keep native shell expansion.

The default working directory is the script's parent, or the selected project
for `uv-project`. It is separate from choosing a Python interpreter or project.
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
```

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
