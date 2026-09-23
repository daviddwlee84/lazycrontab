# SSH hosts and authentication

An SSH alias is the name in a command such as `ssh lab`. OpenSSH owns its address,
user, keys, ProxyJump route and host-key policy. lazycrontab only stores which
aliases you want to manage and optional cron-specific settings:

```toml
[[hosts]]
id = "lab"
ssh = "lab"
```

Changes to that alias's address or key in SSH config take effect on subsequent
connections. Removing its lazycrontab registration does not remove SSH config or
remote jobs. Each registered host has a user-crontab source automatically; add an
explicit file source when you want to manage a Supercronic file.

## Choose hosts

Run `lazycrontab hosts add`, or use the dashboard's add-host action. The picker
starts with exact aliases from your local SSH config, with no preselection.
Search with `/`, choose with Space or the checkboxes, then click **Add selected**
or press `Ctrl+S`. After the local save, independent read-only checks report each
host's readiness. A failed check leaves its registration available for later use.
Click a result row or use arrows to select it, then **Authenticate** / `a` or
**Retry** / `r`. Enter returns to the dashboard.

Discovery does not run SSH, contact servers, query an agent, or evaluate
`Match exec`. Includes are bounded; conditional or unrecognized declarations are
shown as uncertain rather than assumed usable. **Manual alias** lets you explicitly
register an alias when static discovery cannot establish its context. For a
destination such as `user@example.com`, use a separate local ID:

```sh
lazycrontab hosts add lab --ssh user@example.com --dry-run
lazycrontab hosts add lab --ssh user@example.com --yes
lazycrontab hosts discover --from ssh --json
lazycrontab hosts import --from ssh --alias lab,worker --dry-run
lazycrontab hosts import --from ssh --alias lab,worker --yes
```

The explicit flag path does not test connections while registering. Use
`lazycrontab hosts test lab` for a read afterward. Without `--alias`, a
noninteractive `hosts import --yes` registers all new selectable candidates.

## Keys, passwords and connection lifetime

Background reads use OpenSSH's `BatchMode=yes`; they do not open password,
passphrase or host-trust prompts. A configured and authorized key can be used
automatically when it is available to SSH. An encrypted key may first need to be
unlocked in your usual SSH agent or platform keychain. Having a key file alone
does not bypass host-key trust, server requirements or hardware/MFA interaction.

Use **Authenticate** in the dashboard/result page or:

```sh
lazycrontab hosts authenticate lab
```

The terminal goes to native SSH for passwords, key passphrases, MFA and host-key
confirmation. After a successful handoff, lazycrontab performs one background
crontab read. A successful interactive login alone is not reported as proof that
later background connections can authenticate.

Your configured OpenSSH ControlPath policy is retained. When there is no configured
ControlPath, lazycrontab creates a private, app-owned master connection with a
ten-minute **idle** lifetime. Subsequent CLI and dashboard reads can reuse it.
This retains an authenticated connection, not a saved password. Once it expires,
a password-only host may need another explicit authentication. A user-configured
ControlPath without a persistent master may likewise require agent/key setup or
an adjustment to your own SSH sharing policy.

Connection refusal, timeout, host-key failure and authentication refusal are shown
as the errors reported by the attempted read. No failed check removes a host,
weakens host-key policy or triggers repeated prompts. Failed remote writes are
never retried automatically through authentication.

## Optional dev integration

If dev manages your aliases through OpenSSH Includes, those aliases already work
with lazycrontab. Choose **dev** in the picker or `--from dev` to use its static,
versioned `dev ssh list --json` inventory and status information. Only active,
selectable aliases can be picked automatically. Missing dev, an unknown schema
or an incomplete scan is reported; SSH config and manual aliases remain available.

dev can separately store verified reusable SSH passwords in a system credential
provider or Bitwarden. That workflow does not make the password available to
ordinary OpenSSH clients. lazycrontab does not read dev's credential references,
fetch its saved passwords or depend on its private askpass protocol. A prior
`dev ssh connect` session does not establish a shared master for lazycrontab.
Keep credential onboarding in your existing SSH/dev workflow; use native
authentication and short-lived connection reuse here.
