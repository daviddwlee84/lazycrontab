# Paths and script presets

    Choose target -> Choose script -> Runtime and directory -> Schedule -> Review

A script's absolute filename and its working directory solve different problems.
`/srv/report/jobs/daily.py` identifies a file; opening `./data/input.csv` inside
that script still uses the process working directory.

The helper accepts relative paths against a visible base. Local paths start at
the directory where lazycrontab was launched. Remote paths start at the target
HOME or selected directory, never the local machine's directory. The resulting
recipe records absolute paths and an explicit working directory.

Executable/shebang runs the script directly and needs execute permission.
Shell and Python presets use a selected interpreter and need a readable script.
Choose an existing virtualenv interpreter to use its dependencies. Script
arguments are distinct values; quoting preserves spaces and literal symbols.

Checks inspect paths, permissions, runtime availability and project markers.
They do not execute the script, import its modules, install packages, or load
shell profiles. Missing and unverified dependencies remain visible findings.
You can fix a finding, keep the draft, or save the job disabled for later.
Run now is a separate explicit action that really executes the job.

Advanced output settings append stdout/stderr to chosen files. Check their
parent directories and permissions. Pueue is optional: choosing it enqueues the
command when cron triggers; actual execution may wait in the queue.

Raw shell commands remain available. Existing --script metadata does not
silently replace a command with a generated script preset.
