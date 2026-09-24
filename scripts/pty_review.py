#!/usr/bin/env python3
"""Reviewed dashboard mutations in real PTYs with a private crontab fixture.

Usage: python3 scripts/pty_review.py /absolute/path/to/lazycrontab
Only Python's standard library is required. No scheduled command is executed.
"""
from contextlib import contextmanager
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile

from pty_smoke import Session


def prepare_fixture(root):
    """Also usable by local VHS visual checks; everything stays under root."""
    root = Path(root)
    bin_dir = root / "bin"
    bin_dir.mkdir()
    cron = root / "crontab"
    cron.write_text('# Example schedules\nPATH=/usr/bin:/bin\n'
                    '# lazycrontab: {"v":1,"id":"first","name":"First task"}\n'
                    '0 9 * * * echo first\n'
                    '# lazycrontab: {"v":1,"id":"backup","name":"Backup job","remark":"Daily project backup"}\n'
                    '15 3 * * * echo backup\n')
    backend = bin_dir / "crontab"
    backend.write_text('''#!/bin/sh
case "$1" in
 -l) cat "$FIXTURE_CRON";;
 -) cat > "$FIXTURE_CRON"; printf 'write\\n' >> "$FIXTURE_WRITES";;
 *) exit 2;;
esac
''')
    backend.chmod(0o700)
    env = dict(os.environ)
    env.update({"HOME": str(root), "TERM": "xterm-256color", "PATH": str(bin_dir) + ":/usr/bin:/bin",
                "FIXTURE_CRON": str(cron), "FIXTURE_WRITES": str(root / "writes")})
    for key in ("LAZYCRONTAB_CONFIG", "LAZYCRONTAB_HOST", "LAZYCRONTAB_SOURCE", "NO_COLOR", "BASH_ENV", "ENV"):
        env.pop(key, None)
    for category in ("CONFIG", "DATA", "STATE", "CACHE"):
        env[f"XDG_{category}_HOME"] = str(root / category.lower())
    config = root / "config.toml"
    config.write_text('timezone="UTC"\ntheme="dark"\nrefresh_seconds=60\n')
    return env, ["--config", str(config)], cron


@contextmanager
def terminal(binary, args, env, root):
    session = Session(binary, args, env, str(root), cols=180, rows=44)
    try:
        yield session
    finally:
        if session.process.poll() is None:
            rows = subprocess.check_output(["ps", "-axo", "pid,ppid"], text=True).splitlines()[1:]
            owned = {session.process.pid}
            children = []
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
    with tempfile.TemporaryDirectory(prefix="lazycrontab-review-pty-") as tmp:
        root = Path(tmp)
        env, args, cron = prepare_fixture(root)
        initial = cron.read_bytes()

        def writes():
            path = Path(env["FIXTURE_WRITES"])
            return len(path.read_text().splitlines()) if path.exists() else 0

        with terminal(binary, args, env, root) as session:
            session.expect("First task")
            session.send("/Backup\r")
            session.pump(0.25)
            session.expect("Backup job")
            mark = session.mark(); session.send("x")
            session.expect("Status: Enabled", mark)
            session.expect("Disabled", mark)
            session.expect("proposed", mark)

            # A repaint proves that the job/source dashboard remains behind the
            # centered popup; this does not merely match an old terminal frame.
            mark = session.mark(); session.resize(181, 44)
            session.expect("Hosts / sources", mark)
            session.expect("Status: Enabled", mark)
            session.expect("Backup", mark)
            session.send(b"\r"); session.pump(0.2)
            assert cron.read_bytes() == initial and writes() == 0, "Enter approved a default-No review"
            session.click(42, 0)  # Global Playground tab under the overlay.
            session.click(4, 4)   # All sources row under the overlay.
            session.wheel(4, 4)
            session.pump(0.2)
            assert b"F1 Fields" not in session.output[mark:], "popup mouse event leaked into dashboard"
            assert cron.read_bytes() == initial and writes() == 0

            # Small terminals retain safe keyboard behavior and resize never
            # approves the action or loses the selected target.
            session.resize(40, 12); session.pump(0.2)
            session.send(b"\r"); session.pump(0.2)
            assert cron.read_bytes() == initial and writes() == 0
            mark = session.mark(); session.resize(120, 40)
            session.expect("Status: Enabled", mark)
            session.expect("Backup job", mark)
            mark = session.mark(); session.send("yy")
            session.expect("Saved", mark)
            session.expect("Job disabled", mark)
            session.expect("Target:", mark)
            session.expect("Backup:", mark)
            result = session.output[mark:]
            assert b'"status"' not in result and b'"revision"' not in result and b'"backup"' not in result, "human result rendered a JSON receipt"
            assert writes() == 1, "review submitted more than once"
            disabled = cron.read_bytes()
            assert b"# lazycrontab-disabled:" in disabled and b"echo first" in disabled
            # Result acknowledgment also owns events over the background.
            session.click(42, 0); session.pump(0.2)
            assert writes() == 1
            mark = session.mark(); session.send(b"\r")
            session.pump(0.2)
            session.resize(121, 40)
            session.expect("Backup job", mark)
            session.expect("/ Backup", mark)
            session.expect("[ Add job n ]", mark)
            session.expect("Tab focus", mark)
            session.pump(0.4)

            # The same selected/filtered job is now disabled. Cancelling its
            # opposite operation returns to the dashboard without another write.
            mark = session.mark(); session.send("x")
            session.expect("Status: Disabled", mark)
            session.expect("Enabled", mark)
            session.send(b"\x1b"); session.pump(0.3)
            assert cron.read_bytes() == disabled and writes() == 1

            # A stale source is a readable failure, never an automatic retry.
            mark = session.mark(); session.send("x")
            session.expect("Status: Disabled", mark)
            concurrent = disabled + b"# changed by another editor\n"
            cron.write_bytes(concurrent)
            mark = session.mark(); session.send("y")
            session.expect("Not saved", mark)
            session.expect("source changed", mark)
            assert cron.read_bytes() == concurrent and writes() == 1
            assert b'"status"' not in session.output[mark:], "failure rendered a JSON receipt"
            mark = session.mark(); session.send(b"\r")
            session.pump(0.2)
            session.resize(120, 40)
            session.expect("Backup job", mark)
            session.expect("/ Backup", mark)
            session.expect("[ Add job n ]", mark)
            session.expect("Tab focus", mark)
            session.pump(0.4)
            mark = session.mark(); session.send("x")
            session.expect("Status: Disabled", mark)
            session.send(b"\x1b"); session.pump(0.2)
            session.close()
        assert writes() == 1
        print("Review PTY passed: dashboard-backed toggle popup, default-No Enter, outside mouse containment, narrow/wide resize, one apply, human saved/error receipts, retained selection/filter, cancel and conflict safeguards, terminal restoration")


if __name__ == "__main__":
    main()
