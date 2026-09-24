# SSH hosts and sources

    Existing SSH alias -> Add selected hosts -> Read user crontab

OpenSSH owns hostname, user, keys, jump hosts and trust settings. lazycrontab
stores only which aliases you want to see and cron-specific settings. A host
normally needs just its alias; the user crontab source is provided automatically.
Pueue and Supercronic file sources are optional, separate choices.

hosts add searches existing SSH aliases without connecting. Only selected hosts
are registered. All in the dashboard means all registered hosts, not every alias
in ~/.ssh/config. Included config files are inspected statically; conditional
or conflicting candidates are identified rather than evaluated by running code.
dev is an optional inventory source, not a required SSH transport.

Keys and ssh-agent work through normal OpenSSH. Background reads use BatchMode
and cannot prompt. Authenticate & retry hands the terminal to native SSH for a
password, key passphrase, MFA or host-key decision. Successful authentication
is followed by a real background read before the host is reported ready.

Your configured connection-sharing policy takes precedence. Without one,
lazycrontab can reuse its own private connection for ten idle minutes. It stores
no password. dev's private Keychain/Bitwarden credential integration is not a
global OpenSSH password provider and is not read by lazycrontab.

Remote timezone is detected when possible; an advanced override is available.
Unknown timezone does not mean UTC. A source represents a user crontab or an
explicit cron file; system sources remain read-only.

Use v to inspect the selected source's original text and V to edit it through
your local editor. SSH sources are downloaded as private drafts; changes reach
the remote host only after the diff is approved. In All, the selected job chooses
the source; choose a specific source to inspect an empty crontab. / finds text,
arrows scroll/pan and Esc returns. Read-only sources cannot be edited.

CLI: sources show prints original bytes; sources edit-raw opens the editor or
uses --file LOCAL_FILE with --dry-run/--yes. sources edit changes registration.
Changing a raw command may invalidate its helper recipe. Backups and source
revision checks still apply; editing a Supercronic file does not reload it.
