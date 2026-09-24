#!/usr/bin/env python3
"""Native zsh Tab acceptance with private completion/config/backend fixtures.

Usage: python3 scripts/pty_completion.py /absolute/path/to/lazycrontab
Requires zsh plus Python's standard library; does not read user shell startup
files or install completion globally. Candidate lookup must stay offline.
"""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile

from pty_smoke import Session


@contextmanager
def terminal(shell, args, env, root):
    session = Session(shell, args, env, str(root), cols=160, rows=30)
    try:
        yield session
    finally:
        if session.process.poll() is None:
            try:
                os.killpg(session.process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            session.process.wait(timeout=5)
        for fd in (session.master, session.slave):
            try:
                os.close(fd)
            except OSError:
                pass


def main():
    binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else "./lazycrontab").resolve())
    zsh = shutil.which("zsh")
    if not zsh:
        raise SystemExit("zsh is required for real Tab verification; no shell packages are installed by this script")
    bash = shutil.which("bash")
    with tempfile.TemporaryDirectory(prefix="lazycrontab-completion-pty-") as tmp:
        root = Path(tmp)
        bin_dir, functions = root / "bin", root / "fpath"
        bin_dir.mkdir()
        functions.mkdir()
        (bin_dir / "lazycrontab").symlink_to(binary)
        local, remote = root / "local.cron", root / "remote.cron"
        local.write_text('# lazycrontab: {"v":1,"id":"seed-local","name":"Local fixture"}\n* * * * * echo local\n')
        remote.write_text('# lazycrontab: {"v":1,"id":"remote-seed","name":"Remote fixture"}\n0 * * * * echo remote\n')
        blocked, calls = root / "forbid-backends", root / "backend-calls"
        guard = '''if [ -e "$FIXTURE_BLOCK_BACKENDS" ]; then
  printf '%s\\n' "$0 $*" >> "$FIXTURE_BACKEND_CALLS"
  exit 97
fi
'''
        fixtures = {
            "crontab": 'case "$1" in -l) cat "$FIXTURE_CRON";; *) exit 98;; esac\n',
            "ssh": '''if [ "$1" = -G ]; then printf 'controlpath none\\n'; exit; fi
for last do :; done
FIXTURE_CRON=$FIXTURE_REMOTE_CRON
export FIXTURE_CRON
exec sh -c "$last"
''',
            "dev": "exit 98\n",
            "pueue": "exit 98\n",
        }
        for name, body in fixtures.items():
            path = bin_dir / name
            path.write_text("#!/bin/sh\n" + guard + body)
            path.chmod(0o700)
        env = dict(os.environ)
        env.update({
            "HOME": tmp, "ZDOTDIR": tmp, "TERM": "xterm-256color",
            "PATH": str(bin_dir) + ":/usr/bin:/bin", "HISTFILE": str(root / "history"),
            "FIXTURE_CRON": str(local), "FIXTURE_REMOTE_CRON": str(remote),
            "FIXTURE_BLOCK_BACKENDS": str(blocked), "FIXTURE_BACKEND_CALLS": str(calls),
        })
        for key in ("LAZYCRONTAB_CONFIG", "LAZYCRONTAB_HOST", "LAZYCRONTAB_SOURCE", "NO_COLOR", "ENV", "BASH_ENV", "FPATH"):
            env.pop(key, None)
        for category in ("CONFIG", "DATA", "STATE", "CACHE"):
            env[f"XDG_{category}_HOME"] = str(root / category.lower())
        config = root / "config" / "lazycrontab" / "config.toml"
        config.parent.mkdir(parents=True)
        config.write_text('timezone="UTC"\n[[hosts]]\nid="lab"\nssh="fixture-lab"\ntimezone="UTC"\n'
                          f'[[sources]]\nid="finance"\nhost="local"\nkind="file"\npath={json.dumps(str(root / "finance.cron"))}\n'
                          f'[[sources]]\nid="demo"\nhost="local"\nkind="file"\npath={json.dumps(str(root / "demo.cron"))}\n'
                          f'[[sources]]\nid="remoteWork"\nhost="lab"\nkind="file"\npath={json.dumps(str(root / "remote-work.cron"))}\n')
        (root / "broken.toml").write_text("[malformed\n")
        (root / "stray-file-must-not-complete").write_text("completion must not offer this as an ID\n")

        def run(*args):
            return subprocess.run([binary, *args], env=env, cwd=tmp, check=True, capture_output=True).stdout

        # Cache comes from explicit real reads. Once armed, the wrappers fail and
        # record any accidental refresh triggered by candidate discovery.
        run("list", "--json")
        run("--host", "lab", "list", "--json")
        blocked.touch()
        zsh_completion = functions / "_lazycrontab"
        zsh_completion.write_bytes(run("completion", "zsh"))
        bash_completion = root / "lazycrontab.bash"
        bash_completion.write_bytes(run("completion", "bash"))
        subprocess.run([zsh, "-n", str(zsh_completion)], env=env, check=True, capture_output=True)
        if bash:
            subprocess.run([bash, "-n", str(bash_completion)], env=env, check=True, capture_output=True)

        # Load setup from our private ZDOTDIR, never from typed input: otherwise
        # the readiness marker can match the echoed command before compinit has
        # finished. Only zle-line-init announces that the editor owns input.
        (root / ".zshrc").write_text(
            f"fpath=({shlex.quote(str(functions))} $fpath)\n"
            # Ignore insecure inherited function directories; compinit's prompt
            # must never consume the first character of a completion case.
            "autoload -Uz compinit; compinit -i -D || exit; bindkey -e\n"
            "unsetopt beep; PS1='COMPLETE> '\n"
            "_capture_buffer() { zle -I; print -r -- \"__BUFFER_BEGIN__${BUFFER}__BUFFER_END__\"; "
            "BUFFER=''; CURSOR=0; zle reset-prompt; }\n"
            "zle -N _capture_buffer; bindkey '^G' _capture_buffer\n"
            "_completion_ready() { zle -I; print -r -- '__COMPLETION_READY__'; }\n"
            "zle -N zle-line-init _completion_ready\n"
        )
        # -d skips global startup files; HOME/ZDOTDIR both name the fixture.
        with terminal(zsh, ["-d", "-i"], env, root) as session:
            session.expect("__COMPLETION_READY__")

            def complete(line, expected):
                mark = len(session.output)
                # ZLE processes capture after the preceding real Tab widget
                # returns, even when candidate discovery is slow.
                session.send(line + "\t\x07")
                session.expect("__BUFFER_END__", mark)
                matches = re.findall(rb"__BUFFER_BEGIN__(.*?)__BUFFER_END__", session.output[mark:], re.S)
                assert matches, session.output[mark:]
                actual = matches[-1].decode().rstrip()
                assert actual == expected.rstrip(), f"Tab for {line!r}: expected {expected!r}, got {actual!r}"
                assert not calls.exists(), f"Tab called a backend: {calls.read_text()}"

            for line, expected in (
                ("lazycrontab sources edit fi", "lazycrontab sources edit finance"),
                ("lazycrontab --host lab sources edit re", "lazycrontab --host lab sources edit remoteWork"),
                ("lazycrontab hosts edit la", "lazycrontab hosts edit lab"),
                ("lazycrontab --host la", "lazycrontab --host lab"),
                ("lazycrontab --host lab --source re", "lazycrontab --host lab --source remoteWork"),
                ("lazycrontab --host lab sources edit fi", "lazycrontab --host lab sources edit fi"),
                ("lazycrontab sources edit finance stray", "lazycrontab sources edit finance stray"),
                ("lazycrontab sources edit stray", "lazycrontab sources edit stray"),
                ("lazycrontab edit se", "lazycrontab edit seed-local"),
                ("lazycrontab --host lab edit remote-s", "lazycrontab --host lab edit remote-seed"),
                ("lazycrontab --host lab edit se", "lazycrontab --host lab edit se"),
                ("lazycrontab add --runner pu", "lazycrontab add --runner pueue"),
                ("lazycrontab add --output-policy st", "lazycrontab add --output-policy stderr-only"),
                ("lazycrontab edit seed-local --enqueue-output qu", "lazycrontab edit seed-local --enqueue-output quiet"),
                ("lazycrontab add --preset managed-s", "lazycrontab add --preset managed-shell"),
                ("lazycrontab sources add extra --kind sy", "lazycrontab sources add extra --kind system"),
                ("lazycrontab --config broken.toml so", "lazycrontab --config broken.toml sources"),
                ("lazycrontab --config broken.toml --color ne", "lazycrontab --config broken.toml --color never"),
                ("lazycrontab --config broken.toml sources edit stray", "lazycrontab --config broken.toml sources edit stray"),
                ("lazycrontab completion z", "lazycrontab completion zsh"),
            ):
                complete(line, expected)
            config.rename(config.with_suffix(".saved"))
            complete("lazycrontab sources edit u", "lazycrontab sources edit user")
            complete("lazycrontab --host lo", "lazycrontab --host local")
            session.close("exit\n")
        assert not calls.exists(), f"completion invoked live backend: {calls.read_text()}"
        print("Completion PTY passed: native zsh Tab insertion for target-aware IDs, cached job IDs, enums and flags; no filesystem fallback after IDs; missing/malformed config; offline backend guard; generated zsh/Bash syntax; shell terminal restoration")
        print("Native Bash Tab not exercised: this harness verifies zsh; generated Bash completion was syntax-checked." if bash else "Bash syntax check skipped: bash is unavailable.")


if __name__ == "__main__":
    main()
