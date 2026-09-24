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


def check_add_popup(binary):
    with tempfile.TemporaryDirectory(prefix="lazycrontab-draft-pty-") as tmp:
        root = Path(tmp)
        env, args, cron = prepare_fixture(root)
        before = cron.read_bytes()
        command = "echo 'j q 1 2 / V'"
        with terminal(binary, args, env, root) as session:
            session.expect("First task")
            mark = session.mark(); session.send("n")
            session.expect("add job", mark)
            mark = session.mark(); session.resize(181, 44)
            session.expect("Hosts / sources", mark)
            session.expect("add job", mark)
            session.send("\t\tPopup draft\t\t" + command)
            session.pump(0.3)
            session.resize(180, 44)
            session.expect(command)
            # Drafts own printable keys and Alt combinations. Page navigation
            # is available only after closing this interaction.
            mark = session.mark(); session.click(42, 0); session.click(4, 4)
            session.pump(0.2)
            assert b"F1 Fields" not in session.output[mark:]
            mark = session.mark(); session.send(b"\x1b3\x1b2\x1b1"); session.pump(0.3)
            assert b"F1 Fields" not in session.output[mark:] and b"Forecast" not in session.output[mark:]
            session.resize(181, 44)
            session.expect("add job", mark)
            session.expect(command, mark)

            mark = session.mark(); session.send(b"\x1bOP")
            session.expect("Concepts", mark)
            session.click(2, 35)  # Beside nested Help's footer, outside the frame.
            mark = session.mark(); session.resize(180, 44)
            session.expect("Concepts", mark)
            mark = session.mark(); session.send(b"\x1b")
            session.expect("add job", mark)
            session.expect(command, mark)
            # Picker, schedule and multiline tools borrow the draft frame;
            # closing each returns to the containing draft, never the dashboard.
            mark = session.mark(); session.send(b"\t\x10")
            session.expect("Choose Working directory", mark)
            mark = session.mark(); session.send(b"\x1b")
            session.expect("add job", mark)
            mark = session.mark(); session.send(b"\t\r")
            session.expect("F1 Fields", mark)
            mark = session.mark(); session.send("u")
            session.expect("add job", mark)
            session.send(b"\x1b[Z" * 3 + b"\x1b[D\t\r")
            session.expect("saved after job review and Apply")
            body = b'echo "managed draft"\necho "100%"\n'
            session.send(b"\x1b[200~" + body + b"\x1b[201~")
            mark = session.mark(); session.send(b"\x13")
            session.expect("add job", mark)
            session.send(b"\x1b[Z\x1b[C\t")
            session.pump(0.2)
            session.expect(command)

            # A one-field viewport still reaches every basic/advanced field.
            session.resize(40, 12); session.pump(0.2)
            tiny_width = 40
            def next_field(label):
                nonlocal tiny_width
                mark = session.mark(); session.send(b"\t"); session.pump(0.1)
                tiny_width = 81 - tiny_width
                session.resize(tiny_width, 12)
                session.expect(label, mark)
            for label in ("Working directory", "Schedule", "Enabled", "Remark"):
                next_field(label)
            session.send("popup remark")
            mark = session.mark(); session.send(b"\x0f")
            session.expect("directly or enqueue", mark)
            for label in ("Variables", "Append output", "Separate error", "Existing log", "Host"):
                next_field(label)
            mark = session.mark(); session.wheel(20, 6)
            session.expect("Source", mark)
            session.wheel(20, 6, down=False); session.pump(0.2)
            mark = session.mark(); session.resize(180, 44)
            session.expect("Hosts / sources", mark)
            session.expect("Popup draft", mark)
            session.expect(command, mark)
            mark = session.mark(); session.send(b"\x13")
            session.expect("Task: Shell command", mark)
            session.expect(command, mark)
            mark = session.mark(); session.send(b"\x1b")
            session.expect("add job", mark)
            session.expect("Popup draft", mark)
            session.expect(command, mark)
            session.send(b"\x1b"); session.pump(0.3)
            assert cron.read_bytes() == before and not Path(env["FIXTURE_WRITES"]).exists()
            assert not list((root / "data" / "lazycrontab" / "scripts").glob("*/*.sh")), "cancel published managed draft"

            mark = session.mark(); session.send("n")
            session.expect("add job", mark)
            session.send("\t\tPopup saved\t\techo popup_saved")
            mark = session.mark(); session.send(b"\x13")
            session.expect("Task: Shell command", mark)
            session.send(b"\r"); session.pump(0.2)
            assert not Path(env["FIXTURE_WRITES"]).exists()
            mark = session.mark(); session.send("yy")
            session.expect("Saved", mark)
            session.expect("Job added", mark)
            assert Path(env["FIXTURE_WRITES"]).read_text().splitlines() == ["write"]
            assert b"echo popup_saved" in cron.read_bytes() and before in cron.read_bytes()
            session.send(b"\r"); session.pump(0.4)
            mark = session.mark(); session.resize(181, 44)
            session.expect("First task", mark)
            session.expect("Popup saved", mark)
            session.expect("Tab focus", mark)
            session.close()
        # Standalone Playground hosts its Add workflow on the whole terminal;
        # merely embedding a form does not opt into the dashboard's popup frame.
        with terminal(binary, [*args, "playground"], env, root) as session:
            session.expect("F1 Fields")
            mark = session.mark(); session.send("u")
            session.expect("add job", mark)
            session.expect("0 9 * * 1-5", mark)
            assert b"Job draft" not in session.output[mark:], "standalone Playground acquired dashboard popup geometry"
            session.send("\t\tStandalone draft")
            mark = session.mark(); session.send(b"\x1b")
            session.expect("F1 Fields", mark)
            session.expect("0 9 * * 1-5", mark)
            session.close(b"\x1b")
        print("Draft PTY passed: Jobs Add popup/backdrop, modal input ownership, nested help/picker/schedule/multiline, all fields at narrow sizes, wheel, review Back, cancel without target files, one save, terminal restoration")


def check_advanced_and_mouse(binary):
    with tempfile.TemporaryDirectory(prefix="lazycrontab-advanced-pty-") as tmp:
        root = Path(tmp)
        env, args, cron = prepare_fixture(root)
        before = cron.read_bytes()
        with terminal(binary, args, env, root) as session:
            session.resize(180, 60)
            session.expect("First task")
            session.send("n"); session.expect("add job")
            # No resize or navigation after expansion: new fields and the
            # focused runner must appear immediately in the enlarged frame.
            mark = session.mark(); session.send(b"\x0f")
            session.expect("Run directly or enqueue", mark)
            session.expect("Existing log to inspect", mark)
            session.expect("14/14", mark)
            session.send("\tSAMPLE=kept")
            session.pump(0.2)
            mark = session.mark(); session.send(b"\x0f")
            session.expect("Advanced ^O", mark)
            mark = session.mark(); session.send(b"\x0f")
            session.expect("Run directly or enqueue", mark)
            session.expect("SAMPLE=kept", mark)

            # A capped frame focuses the same new field and exposes an honest
            # visible range. Collapsing and reopening preserves entered values.
            session.send(b"\x0f"); session.pump(0.2)
            session.resize(40, 12); session.pump(0.2)
            mark = session.mark(); session.send(b"\x0f")
            session.expect("directly or enqueue", mark)
            session.expect("10–10/14", mark)
            mark = session.mark(); session.send(b"\t")
            session.expect("SAMPLE=kept", mark)
            session.send(b"\x1b"); session.pump(0.2)
            session.close()
        assert cron.read_bytes() == before and not Path(env["FIXTURE_WRITES"]).exists()

    for preference in ("default", "off", "custom-m"):
        with tempfile.TemporaryDirectory(prefix="lazycrontab-mouse-pty-") as tmp:
            root = Path(tmp)
            env, args, cron = prepare_fixture(root)
            config = Path(args[1])
            if preference == "off":
                config.write_text(config.read_text() + "mouse=false\n")
            elif preference == "custom-m":
                config.write_text(config.read_text() + '[keys]\nplayground="m"\n')
            with terminal(binary, args, env, root) as session:
                session.expect("First task"); session.pump(0.2)
                capture_enabled = b"\x1b[?1002h" in session.output
                assert capture_enabled == (preference != "off"), f"wrong configured mouse capture: {preference}"
                mark = session.mark(); session.send("m"); session.pump(0.2)
                if preference == "custom-m":
                    session.expect("F1 Fields", mark)
                    assert b"\x1b[?1002l" not in session.output[mark:], "custom m toggled mouse capture"
                else:
                    assert b"\x1b[?1002h" not in session.output[mark:] and b"\x1b[?1002l" not in session.output[mark:], "m still changes mouse mode"
                    mark = session.mark(); session.click(42, 0); session.pump(0.2)
                    if preference == "default":
                        session.expect("F1 Fields", mark)
                    else:
                        assert b"F1 Fields" not in session.output[mark:], "mouse=false accepted a click"
                        session.send("3"); session.expect("F1 Fields", mark)
                session.close()
            if preference != "custom-m":
                with terminal(binary, [*args, "disable", "first"], env, root) as session:
                    session.expect("Review"); session.expect("disable"); session.pump(0.2)
                    capture_enabled = b"\x1b[?1002h" in session.output
                    assert capture_enabled == (preference == "default"), f"CLI confirmation ignored mouse preference: {preference}"
                    session.close(b"\x1b")
            assert not Path(env["FIXTURE_WRITES"]).exists()
    print("Advanced/mouse PTY passed: expansion grows without resize and focuses new fields, narrow range and preserved values, default/on and configured off capture, no m toggle, custom m binding")


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
    check_add_popup(binary)
    check_advanced_and_mouse(binary)


if __name__ == "__main__":
    main()
