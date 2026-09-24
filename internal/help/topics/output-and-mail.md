# Output and cron mail

    Cron triggers -> Run directly or enqueue -> Task output

In Advanced, Task output chooses what happens to the task's stdout and stderr.
The CLI equivalent is --output-policy:

- inherit: keep the runner's normal handling. Native cron receives direct-job
  output; Supercronic logs it. Pueue captures queued-task output.
- files: append to your chosen files. --output PATH combines stdout and stderr;
  --stderr PATH sends stderr to a separate file. With only --stderr, stdout
  keeps its normal destination. At least one path is required.
- stderr-only: discard stdout and keep stderr with the runner.
- discard: discard both task streams. The process still has an exit status.

inherit is the default. Existing --output/--stderr flags select files when no
policy is supplied; clearing the last path through those CLI flags returns to
inherit. In the form, choose Inherit to stop redirecting. File fields keep their
position when another policy disables them; switching choices keeps their draft
values. Inactive paths do not change the generated command.

Choose file paths on the selected host. Their parent directories must exist and
be writable. lazycrontab does not provide managed log storage or log rotation.
Existing log to inspect (--log) is a separate read path, not an output redirect.
Redirected Pueue task output goes to those files instead of Pueue's capture.

stderr-only does not mean failures only: a successful program can write warnings
to stderr, and a failing program can be silent or report errors on stdout.
Discarding output does not turn a failure into success.

## Pueue has two kinds of output

Cron first calls pueue add. Later, the Pueue daemon runs the queued task. Its
task output and the add command's notices are separate.

Enqueue notices (--enqueue-output) controls the outer add command:

- quiet: hide its stdout, including the task ID; keep stderr and its exit status.
- inherit: leave its stdout and stderr for cron or Supercronic.

New Pueue jobs and jobs switched from direct to Pueue default to quiet. Existing
Pueue jobs keep their installed behavior until you explicitly review a change.
quiet avoids routine task-ID mail, but stderr warnings can still produce mail.
Even Task output: discard keeps enqueue stderr visible. It does not hide a
missing Pueue executable, an unreachable daemon or another enqueue failure.

Manual Run retains task-ID feedback for a generated job whose saved helper
recipe still matches its installed command. If the recipe is missing or no
longer matches, the stored command is used without removing its redirects.
An enqueue success means queued, not completed. Inspect the actual task with
pueue log TASK_ID or lazypueue; its output and result belong to Pueue.

## Why a new terminal says "You have new mail"

Native cron can mail captured command output to the crontab owner. The local
mail service delivers it, and your shell may detect the mailbox and print that
notice. It is separate from the macOS Mail app. Linux can use the same mechanism;
the mail service and mailbox location depend on the host's setup.

Output can produce mail even after a successful exit. A nonzero exit by itself
does not guarantee a message. lazycrontab reports the source's mail setting,
not whether the host's mail service can actually deliver anything.

MAILTO is a crontab assignment, applying to following jobs until another MAILTO
assignment changes it:

- Unset: native cron normally uses the crontab owner.
- MAILTO="": disable output mail for the following jobs.
- MAILTO="address": request delivery to that recipient.

Use v to inspect the selected raw source and V to edit a private draft, then
review the exact source diff before applying it. CLI: sources edit-raw. Put a
MAILTO assignment at the top only if you intend it to affect every following
job. Read-only system sources can be inspected but cannot be edited here.

Variables in a job, including --env 'MAILTO=', are passed to the job process;
they do not change cron's own mail setting. lazycrontab does not automatically
set MAILTO, clear your mailbox, or change shell/mail-service configuration.

Supercronic sends task output and status to its logs. Treat its daemon/container
logs as the destination; do not rely on native cron MAILTO behavior there.
Discarding task streams does not remove Supercronic's own scheduler/status logs.

See also: execution-environment, paths-and-scripts
