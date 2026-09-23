# The cron execution environment

Your terminal and the cron daemon do not share the same startup environment.
Native cron normally runs a noninteractive shell. It does not read `~/.zshrc`.
Choosing zsh alone does not make it interactive; `zsh -lc` reads login files,
not `.zshrc`. Shell aliases and functions from an interactive terminal are not
reliable scheduled commands.

Choose the runtime executable, working directory and needed environment
variables explicitly. A Python virtualenv can be used by selecting its
`.venv/bin/python`; no activation script is required. A script with
`#!/usr/bin/env python3` still depends on the scheduled job's PATH.

The native manual-run preview uses target HOME, USER/LOGNAME, SHELL=/bin/sh,
PATH=/usr/bin:/bin, and the applicable crontab assignments. Actual daemon PATH,
PAM settings, limits and permissions can differ. A successful check is not a
guarantee that arbitrary user code will run successfully.

Per-job environment values are literal. Writing `$PATH:/new/path` is not an
instruction to expand another PATH. Review the complete intended value.

Supercronic inherits the environment of its own daemon/container. A file alone
does not reveal that environment. Manual runs and checks on the selected host
cannot recreate an unrelated container process. Unknown context is shown as
unknown, not as a successful verification.

See also: paths-and-scripts, uv
