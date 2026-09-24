#!/usr/bin/env python3
"""Raw-source acceptance in real PTYs, using only private cron/SSH fixtures.

Usage: python3 scripts/pty_raw_source.py /absolute/path/to/lazycrontab
Only Python's standard library is required. No actual crontab is accessed.
"""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile

from pty_smoke import Session


@contextmanager
def terminal(binary, args, env, root):
    session = Session(binary, args, env, str(root), cols=120, rows=40)
    try:
        yield session
    finally:
        if session.process.poll() is None:
            rows = subprocess.check_output(["ps", "-axo", "pid,ppid"], text=True).splitlines()[1:]
            children, owned = [], {session.process.pid}
            for _ in range(8):
                for row in rows:
                    pid, parent = map(int, row.split())
                    if parent in owned and pid not in owned:
                        children.append(pid)
                        owned.add(pid)
            for pid in [*reversed(children), session.process.pid]:
                try:
                    os.kill(pid, signal.SIGKILL)
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
    with tempfile.TemporaryDirectory(prefix="lazycrontab-raw-pty-") as tmp:
        root = Path(tmp)
        bin_dir = root / "bin"
        bin_dir.mkdir()
        local, remote = root / "local.cron", root / "remote.cron"
        empty, remote_empty, system = root / "empty.cron", root / "remote-empty.cron", root / "system.cron"
        local_raw = (b'# LOCAL_RAW_HEADER\r\n# indentation\tand tab\nMAILTO="cron@example.test"\nPATH=/usr/bin:/bin\n'
                     + b"# LONG_LINE " + b"x" * 220 + b" FAR_RIGHT_MARKER\n"
                     + b'# lazycrontab: {"v":1,"id":"local-job","name":"Local visible task"}\n'
                     + b"*/5 * * * * printf 'local \\%s' hi\n"
                     + b"".join(f"# numbered comment {i:02d}\n".encode() for i in range(80))
                     + b"# BOTTOM_MARKER\n")
        remote_raw = (b'# REMOTE_RAW_HEADER\nSHELL=/bin/sh\n'
                      b'# lazycrontab: {"v":1,"id":"remote-job","name":"Remote visible task"}\n'
                      b"0 8 * * * echo remote\n")
        system_raw = b"# READONLY_SYSTEM_HEADER\nMAILTO=root\n0 4 * * * root echo system\n"
        local.write_bytes(local_raw)
        remote.write_bytes(remote_raw)
        empty.write_bytes(b"")
        remote_empty.write_bytes(b"")
        system.write_bytes(system_raw)
        fixtures = {
            "crontab": '''#!/bin/sh
case "$1" in
 -l) cat "$FIXTURE_CRON";;
 -) cat > "$FIXTURE_CRON";;
 *) exit 2;;
esac
''',
            "ssh": '''#!/bin/sh
if [ "$1" = -G ]; then printf 'controlpath none\\n'; exit; fi
printf '%s\\n' "$@" >> "$FIXTURE_SSH_CALLS"
for last do :; done
FIXTURE_CRON=$FIXTURE_REMOTE_CRON
export FIXTURE_CRON
exec sh -c "$last"
''',
            "fixture-editor": '''#!/bin/sh
for last do :; done
[ "$(ls -ld "$last" | cut -c2-10)" = 'rw-------' ] || exit 91
if [ -f "$FIXTURE_EDITOR_EXPECTED" ]; then
  cmp "$last" "$FIXTURE_EDITOR_EXPECTED" || exit 92
  [ "$last" = "$(cat "$FIXTURE_EDITOR_EXPECTED_PATH")" ] || exit 93
fi
stty -a > "$FIXTURE_EDITOR_TTY"
printf '%s' "$last" > "$FIXTURE_EDITOR_DRAFT"
cat "$last" > "$FIXTURE_EDITOR_BEFORE"
cat "$FIXTURE_EDITOR_CONTENT" > "$last"
if [ -n "$FIXTURE_CONFLICT_TARGET" ]; then printf '# CONCURRENT_CHANGE\\n' >> "$FIXTURE_CONFLICT_TARGET"; fi
echo 'RAW EDITOR HANDOFF'
''',
        }
        for name, body in fixtures.items():
            path = bin_dir / name
            path.write_text(body)
            path.chmod(0o700)
        env = dict(os.environ)
        env.update({
            "HOME": tmp, "TERM": "xterm-256color", "PATH": str(bin_dir) + ":/usr/bin:/bin",
            "VISUAL": str(bin_dir / "fixture-editor"), "EDITOR": str(bin_dir / "fixture-editor"),
            "FIXTURE_CRON": str(local), "FIXTURE_REMOTE_CRON": str(remote),
            "FIXTURE_SSH_CALLS": str(root / "ssh-calls"),
            "FIXTURE_EDITOR_CONTENT": str(root / "editor-content.cron"),
            "FIXTURE_EDITOR_BEFORE": str(root / "editor-before.cron"),
            "FIXTURE_EDITOR_DRAFT": str(root / "editor-draft-path"),
            "FIXTURE_EDITOR_TTY": str(root / "editor-tty"),
            "FIXTURE_EDITOR_EXPECTED": str(root / "editor-expected.cron"),
            "FIXTURE_EDITOR_EXPECTED_PATH": str(root / "editor-expected-path"),
            "FIXTURE_CONFLICT_TARGET": "",
        })
        for key in ("LAZYCRONTAB_CONFIG", "LAZYCRONTAB_HOST", "LAZYCRONTAB_SOURCE", "NO_COLOR"):
            env.pop(key, None)
        for category in ("CONFIG", "DATA", "STATE", "CACHE"):
            env[f"XDG_{category}_HOME"] = str(root / category.lower())
        config = root / "config.toml"
        config.write_text('timezone="UTC"\nrefresh_seconds=60\n'
                          '[[hosts]]\nid="lab"\nssh="fixture-lab"\ntimezone="UTC"\n'
                          f'[[sources]]\nid="empty"\nhost="local"\nkind="file"\npath={json.dumps(str(empty))}\ndialect="supercronic"\n'
                          f'[[sources]]\nid="system"\nhost="local"\nkind="system"\npath={json.dumps(str(system))}\n'
                          f'[[sources]]\nid="empty"\nhost="lab"\nkind="file"\npath={json.dumps(str(remote_empty))}\ndialect="supercronic"\n')
        base = ["--config", str(config)]

        def run(*args, check=True):
            return subprocess.run([binary, *base, *args], env=env, cwd=tmp, check=check, capture_output=True)

        def editor_reset(body):
            Path(env["FIXTURE_EDITOR_CONTENT"]).write_bytes(body)
            Path(env["FIXTURE_EDITOR_DRAFT"]).unlink(missing_ok=True)
            Path(env["FIXTURE_EDITOR_EXPECTED"]).unlink(missing_ok=True)
            Path(env["FIXTURE_EDITOR_EXPECTED_PATH"]).unlink(missing_ok=True)

        def check_editor(before):
            assert Path(env["FIXTURE_EDITOR_BEFORE"]).read_bytes() == before, "editor got wrong target bytes"
            modes = Path(env["FIXTURE_EDITOR_TTY"]).read_text()
            assert re.search(r"(?:^|[;\s])icanon(?:[;\s]|$)", modes), modes
            assert re.search(r"(?:^|[;\s])echo(?:[;\s]|$)", modes), modes

        def check_draft_removed():
            draft = Path(Path(env["FIXTURE_EDITOR_DRAFT"]).read_text())
            assert not draft.exists(), "private raw editor draft was left behind"

        def backups():
            return [json.loads(path.read_text()) for path in (root / "state" / "lazycrontab" / "backups").glob("*.json")]

        # Human stdout is exact bytes, including tabs, CRLF and a missing final
        # newline. The JSON form adds targeting facts without changing content.
        for host, source, expected in (("local", "user", local_raw), ("lab", "user", remote_raw), ("local", "system", system_raw), ("lab", "empty", b"")):
            assert run("--host", host, "--source", source, "sources", "show").stdout == expected
            view = json.loads(run("--host", host, "--source", source, "sources", "show", "--json").stdout)
            assert view["host"] == host and view["source"] == source and view["content"].encode() == expected
        empty.write_bytes(b"# final newline deliberately absent")
        assert run("--source", "empty", "sources", "show").stdout == empty.read_bytes()
        empty.write_bytes(b"")

        # A viewer owns its events: tabs and source rows underneath it must not
        # become active after clicks; both scroll axes and resizing remain usable.
        with terminal(binary, base, env, root) as session:
            session.expect("Local visible task")
            mark = session.mark(); session.send("v")
            session.expect("Raw source", mark)
            session.expect("local/user", mark)
            session.expect("LOCAL_RAW_HEADER", mark)
            session.expect('MAILTO="cron@example.test"', mark)
            mark = session.mark(); session.send(b"\x1b[C" * 32)
            session.expect("FAR_RIGHT_MARKER", mark)
            session.send(b"\x1b[D" * 32)
            mark = session.mark(); session.send(b"\x1b[6~" * 4)
            session.expect("BOTTOM_MARKER", mark)
            mark = session.mark(); session.send("/V"); session.pump(0.2)
            assert b"RAW EDITOR HANDOFF" not in session.output[mark:] and not Path(env["FIXTURE_EDITOR_DRAFT"]).exists(), "viewer find treated V as Edit"
            session.send(b"\x1b"); session.pump(0.2)
            session.click(10, 5)
            session.click(42, 0)
            session.wheel(30, 10)
            for size in ((80, 24), (40, 12), (120, 40)):
                session.resize(*size); session.pump(0.2)
            mark = session.mark(); session.send(b"\x1b")
            session.expect("Local visible task", mark)
            # Typing owns V; a filtered empty list still has an explicit source.
            mark = session.mark(); session.send("/zzzzV"); session.pump(0.3)
            assert b"RAW EDITOR HANDOFF" not in session.output[mark:]
            assert not Path(env["FIXTURE_EDITOR_DRAFT"]).exists()
            session.send(b"\r")
            mark = session.mark(); session.send("v")
            session.expect("Raw source", mark)
            session.expect("LOCAL_RAW_HEADER", mark)
            session.send(b"\x1b"); session.pump(0.2)
            session.close()

        # Empty local and remote sources are viewable independently of jobs.
        for host in ("local", "lab"):
            with terminal(binary, [*base, "--host", host, "--source", "empty"], env, root) as session:
                session.expect(f"{host}/empty")
                mark = session.mark(); session.send("v")
                session.expect("Raw source", mark)
                session.expect(f"{host}/empty", mark)
                session.send(b"\x1b"); session.pump(0.2)
                session.close()

        # All uses the selected matching job; an empty filtered fleet must not
        # silently fall back to whichever source happened to be first.
        with terminal(binary, [*base, "--host", "all"], env, root) as session:
            session.expect("Remote visible")
            session.send("/Remote visible\r"); session.pump(0.3)
            mark = session.mark(); session.send("v")
            session.expect("Raw source", mark)
            session.expect("REMOTE_RAW_HEADER", mark)
            session.send(b"\x1b"); session.pump(0.2)
            session.send(b"/\x01\x0bzzzz\r"); session.pump(0.3)
            mark = session.mark(); session.send("vV"); session.pump(0.5)
            assert b"Raw source" not in session.output[mark:] and b"RAW EDITOR HANDOFF" not in session.output[mark:]
            assert not Path(env["FIXTURE_EDITOR_DRAFT"]).exists(), "All without a selected source invoked editor"
            session.close()

        replacement = b'# REPLACEMENT_BYTES\nPATH=/usr/bin:/bin\n# exact\ttab\n*/7 * * * * echo replaced\n'
        editor_reset(replacement)
        # Dashboard V releases terminal ownership, and cancellation keeps the
        # source byte-for-byte unchanged and creates no backup.
        with terminal(binary, base, env, root) as session:
            session.expect("Local visible task")
            mark = session.mark(); session.send("V")
            session.expect("edit-source", mark)
            session.expect("proposed", mark)
            check_editor(local_raw)
            assert local.read_bytes() == local_raw and not backups()
            session.send(b"\r"); session.pump(0.2)
            assert local.read_bytes() == local_raw, "Enter applied default-No review"
            session.send(b"\x1b"); session.pump(0.4)
            session.expect("Local visible task")
            session.close()
        check_draft_removed()
        assert local.read_bytes() == local_raw and not backups()

        # Approval saves the editor's exact bytes and retains the exact original
        # in its ordinary source backup. The SSH fixture routes to a separate file.
        editor_reset(replacement)
        with terminal(binary, [*base, "--host", "lab", "sources", "edit-raw"], env, root) as session:
            session.expect("edit-source")
            session.expect("proposed")
            check_editor(remote_raw)
            assert remote.read_bytes() == remote_raw and not backups()
            mark = session.mark(); session.send("y")
            session.expect("Saved", mark)
            session.close(b"\r")
        check_draft_removed()
        assert remote.read_bytes() == replacement and local.read_bytes() == local_raw
        saved = backups()
        assert len(saved) == 1 and saved[0]["host"] == "lab" and saved[0]["source"] == "user" and saved[0]["content"].encode() == remote_raw

        # A remote change while the editor was open is rejected before install.
        editor_reset(b"# MUST_NOT_INSTALL\n* * * * * echo rejected\n")
        env["FIXTURE_CONFLICT_TARGET"] = str(remote)
        with terminal(binary, [*base, "--host", "lab", "sources", "edit-raw"], env, root) as session:
            session.expect("edit-source")
            session.expect("proposed")
            mark = session.mark(); session.send("y")
            session.expect("source changed", mark)
            session.close(b"\r")
        env["FIXTURE_CONFLICT_TARGET"] = ""
        assert remote.read_bytes() == replacement + b"# CONCURRENT_CHANGE\n" and len(backups()) == 1
        check_draft_removed()

        # Read-only system sources expose viewing while keeping Edit unavailable.
        Path(env["FIXTURE_EDITOR_DRAFT"]).unlink()
        with terminal(binary, [*base, "--source", "system"], env, root) as session:
            session.expect("local/system")
            mark = session.mark(); session.send("v")
            session.expect("READONLY_SYSTEM_HEADER", mark)
            mark = session.mark(); session.send("V"); session.pump(0.3)
            assert b"RAW EDITOR HANDOFF" not in session.output[mark:] and not Path(env["FIXTURE_EDITOR_DRAFT"]).exists()
            session.send(b"\x1b"); session.pump(0.2)
            mark = session.mark(); session.send("V"); session.pump(0.3)
            assert b"RAW EDITOR HANDOFF" not in session.output[mark:]
            assert not Path(env["FIXTURE_EDITOR_DRAFT"]).exists()
            session.close()
        rejected = run("--source", "system", "sources", "edit-raw", "--file", env["FIXTURE_EDITOR_CONTENT"], "--yes", check=False)
        assert rejected.returncode != 0 and b"read-only" in rejected.stderr and system.read_bytes() == system_raw

        # Scriptable replacement has the same diff/revision/backup path, and dry
        # runs never invoke editors or mutate an otherwise empty selected source.
        before_backups = len(backups())
        editor_reset(b"# file replacement\n* * * * * echo file-input\n")
        planned = json.loads(run("--source", "empty", "sources", "edit-raw", "--file", env["FIXTURE_EDITOR_CONTENT"], "--dry-run", "--json").stdout)
        assert planned["after"].encode() == Path(env["FIXTURE_EDITOR_CONTENT"]).read_bytes()
        assert empty.read_bytes() == b"" and len(backups()) == before_backups and not Path(env["FIXTURE_EDITOR_DRAFT"]).exists()
        run("--source", "empty", "sources", "edit-raw", "--file", env["FIXTURE_EDITOR_CONTENT"], "--yes", "--json")
        assert empty.read_bytes() == Path(env["FIXTURE_EDITOR_CONTENT"]).read_bytes() and len(backups()) == before_backups + 1

        # An invalid editor save keeps the same private file for another edit.
        # Repairing permits the ordinary review; neither retry nor cancellation
        # approves installation. The fixture itself verifies retained bytes/path.
        invalid = b"*5 * * * * echo bad\n"
        repaired = b"# repaired draft\n*/5 * * * * echo repaired\n"
        for outcome in ("discard", "review-cancel", "apply"):
            editor_reset(invalid)
            previous, previous_backups = local.read_bytes(), len(backups())
            with terminal(binary, [*base, "sources", "edit-raw"], env, root) as session:
                session.expect("Repair raw source")
                session.expect("local/user")
                session.expect("line 1")
                session.expect("*5")
                draft_path = Path(env["FIXTURE_EDITOR_DRAFT"]).read_text()
                assert Path(draft_path).read_bytes() == invalid and local.read_bytes() == previous and len(backups()) == previous_backups
                if outcome == "discard":
                    session.close(b"\x1b")
                else:
                    Path(env["FIXTURE_EDITOR_EXPECTED"]).write_bytes(invalid)
                    Path(env["FIXTURE_EDITOR_EXPECTED_PATH"]).write_text(draft_path)
                    Path(env["FIXTURE_EDITOR_CONTENT"]).write_bytes(repaired)
                    mark = session.mark(); session.send(b"\r")
                    session.expect("edit-source", mark)
                    session.expect("proposed", mark)
                    check_editor(invalid)
                    assert Path(env["FIXTURE_EDITOR_DRAFT"]).read_text() == draft_path
                    assert local.read_bytes() == previous and len(backups()) == previous_backups
                    if outcome == "review-cancel":
                        session.close(b"\x1b")
                    else:
                        mark = session.mark(); session.send("y")
                        session.expect("Saved", mark)
                        session.close(b"\r")
                check_draft_removed()
            assert local.read_bytes() == (repaired if outcome == "apply" else previous)
            assert len(backups()) == previous_backups + (outcome == "apply")
        assert "fixture-lab" in Path(env["FIXTURE_SSH_CALLS"]).read_text()
        print("Raw-source PTY passed: exact local/SSH bytes, empty/read-only viewing, source targeting and All policy, scroll/resize/modal input, editor handoff and terminal restoration, default-No/cancel, reviewed backup/readback, concurrent edit rejection, invalid-draft repair/discard, file dry-run/apply")


if __name__ == "__main__":
    main()
