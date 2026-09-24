#!/usr/bin/env python3
"""Stable job-form geometry and navigation using real PTYs and private backends.

Usage: python3 scripts/pty_stable_form.py /absolute/path/to/lazycrontab
Only the Python standard library is required. Scheduled commands never run.
"""
from contextlib import contextmanager
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

from pty_review import prepare_fixture
from pty_smoke import Session
from pty_screen import Screen


class FormScreen:
    def __init__(self, session, cols, rows):
        self.session, self.screen, self.offset = session, Screen(cols, rows), 0

    def read(self):
        self.session.pump(0.05)
        self.screen.feed(self.session.output[self.offset:])
        self.offset = len(self.session.output)
        self.lines = self.screen.lines()
        self.box = None
        for y, line in enumerate(self.lines):
            x = line.find("╭ Job draft")
            if x < 0: continue
            right = line.find("╮", x)
            for bottom in range(y + 1, len(self.lines)):
                if self.lines[bottom][x:x + 1] == "╰" and self.lines[bottom][right:right + 1] == "╯":
                    self.box = (x, y, right - x + 1, bottom - y + 1)
                    break
            if self.box: break
        assert self.box, "No draft frame in emitted terminal cells:\n" + "\n".join(self.lines)
        x, y, width, height = self.box
        self.inside = [line[x + 1:x + width - 1] for line in self.lines[y + 1:y + height - 1]]
        return self

    def focus(self):
        for row, line in enumerate(self.inside):
            if line.lstrip().startswith("› "):
                return line.strip()[2:], self.box[1] + row + 1
        raise AssertionError("No focused field:\n" + "\n".join(self.lines))

    def wait_for(self, description, predicate, timeout=12):
        deadline = time.monotonic() + timeout
        last_error = ""
        while time.monotonic() < deadline:
            try:
                self.read()
                if predicate():
                    return
            except AssertionError as error:
                # PTY writes and rendering can split a frame. An incomplete
                # border or an old focused label is not a completed observation.
                last_error = str(error).split("\n", 1)[0]
            if self.session.process.poll() is not None:
                break
        lines = getattr(self, "lines", self.screen.lines())
        raise AssertionError(f"Timed out waiting for {description}; exit={self.session.process.poll()}; {last_error}\n" + "\n".join(lines))

    def expect_focus(self, prefix):
        self.wait_for(f"focus {prefix!r}", lambda: self.focus()[0].startswith(prefix))
        return self.focus()[1]

    def value(self, label):
        for row, line in enumerate(self.inside):
            if label in line:
                return self.inside[row + 1].strip(), self.box[1] + row + 1
        raise AssertionError(f"Missing field {label!r}\n" + "\n".join(self.lines))

    def wait_value(self, label, expected, exact=False):
        self.wait_for(f"{label!r} value {expected!r}", lambda: self.value(label)[0] == expected if exact else self.value(label)[0].startswith(expected))


@contextmanager
def terminal(binary, args, env, root, cols, rows):
    session = Session(binary, args, env, str(root), cols=cols, rows=rows)
    try:
        yield session, FormScreen(session, cols, rows)
    finally:
        if session.process.poll() is None:
            try: os.killpg(session.process.pid, signal.SIGKILL)
            except ProcessLookupError: pass
            session.process.wait(timeout=5)
        for fd in (session.master, session.slave):
            try: os.close(fd)
            except OSError: pass


def install_pueue_fixture(root, env):
    path = root / "bin" / "pueue"
    path.write_text('''#!/bin/sh
case "$1" in
 --version) echo 'pueue 4.0.2';;
 group) echo '{"default":{"status":"Running","parallel_tasks":1},"backup":{"status":"Running","parallel_tasks":1}}';;
 add) echo forbidden > "$FIXTURE_PUEUE_SUBMISSION"; exit 99;;
 *) exit 98;;
esac
''')
    path.chmod(0o700)
    env["FIXTURE_PUEUE_SUBMISSION"] = str(root / "pueue-submission")


def main():
    binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else "./lazycrontab").resolve())
    for cols, rows in ((120, 54), (80, 24), (40, 12)):
        with tempfile.TemporaryDirectory(prefix="lazycrontab-stable-", dir="/tmp") as tmp:
            root = Path(tmp)
            env, args, cron = prepare_fixture(root)
            install_pueue_fixture(root, env)
            before = cron.read_bytes()
            with terminal(binary, args, env, root, cols, rows) as (session, form):
                session.expect("First task")
                session.send("n"); session.expect("add job")
                session.send(b"\t\tStable task\t")
                form.expect_focus("What to run")
                preset, preset_y = form.value("What to run")
                session.send(b"\x1b[B")
                payload_y = form.expect_focus("Command to execute")
                session.send("echo stablejk")
                form.expect_focus("Command to execute")
                form.wait_value("Command to execute", "echo stablejk")
                session.send(b"\x1b[A")
                form.expect_focus("What to run")
                assert form.value("What to run")[0] == preset, "Up/Down changed the selector value"
                session.send(b"\x1b[C\x1b[B")
                assert form.expect_focus("Existing script") == payload_y, "preset moved the shared payload slot"
                session.send(b"\x1b[A\x1b[D\x1b[B")
                form.expect_focus("Command to execute")
                assert form.value("Command to execute")[0].startswith("echo stablejk")
                session.send(b"\x1b[B")
                form.expect_focus("Working directory")
                session.send(b"\x1b[A")
                form.expect_focus("Command to execute")
                if cols == 120:
                    # Reserved inactive runtime/project/args cells stay blank,
                    # and clicking one cannot steal focus from the payload.
                    x, y, width, height = form.box
                    for row in (payload_y + 2, payload_y + 3, payload_y + 4, payload_y + 5):
                        assert not form.lines[row][x + 1:x + width - 1].strip(), "inactive runtime/project slot rendered content"
                    session.click(x + 5, payload_y + 3)
                    form.expect_focus("Command to execute")

                basic_box = form.read().box
                session.send(b"\x0f")
                runner_y = form.expect_focus("Run directly or enqueue")
                form.wait_for("expanded row count", lambda: "/20" in "\n".join(form.inside))
                direct_box = form.box
                assert direct_box[:3] == basic_box[:3], "Advanced changed the popup top/width instead of growing downward"
                if cols == 120:
                    assert "20/20" in "\n".join(form.inside), "expanded reserved rows were not all visible"
                assert "direct" in form.value("Run directly or enqueue")[0]
                session.send(b"\x1b[B")
                form.expect_focus("Variables")  # Skip Pueue group and enqueue notices.
                session.send(b"\x1b[A")
                runner_y = form.expect_focus("Run directly or enqueue")
                assert "direct" in form.value("Run directly or enqueue")[0]
                session.send(b"\x1b[C")
                form.wait_value("Run directly or enqueue", "◀ pueue")
                actual_runner_y = form.expect_focus("Run directly or enqueue")
                assert actual_runner_y == runner_y, f"{cols}x{rows}: runner moved from {runner_y} to {actual_runner_y}; box {direct_box} -> {form.box}\n" + "\n".join(form.lines)
                assert "pueue" in form.value("Run directly or enqueue")[0]
                assert form.box == direct_box, "switching to Pueue moved/resized the popup"
                session.send(b"\x1b[B")
                form.expect_focus("Existing Pueue group")
                form.wait_value("Existing Pueue group", "◀")
                session.send(b"\x1b[A\x1b[D")
                form.wait_value("Run directly or enqueue", "◀ direct")
                assert form.expect_focus("Run directly or enqueue") == runner_y
                assert form.box == direct_box and "direct" in form.value("Run directly or enqueue")[0]
                session.send(b"\x1b[C")
                form.wait_value("Run directly or enqueue", "◀ pueue")
                session.send(b"\x1b[B\x1b[B")
                form.expect_focus("Enqueue notices")
                session.send(b"\x1b[B")
                form.expect_focus("Variables")
                if cols == 120:
                    _, output_y = form.value("Append output")
                    session.click(form.box[0] + 7, output_y + 1)
                    form.expect_focus("Variables")
                session.send(b"\x1b[B")
                policy_y = form.expect_focus("Task output")
                policy_box = form.box
                session.send(b"\x1b[C")  # File routing enables empty path inputs.
                assert form.expect_focus("Task output") == policy_y
                assert form.box == policy_box, "output policy moved/resized the popup"
                session.send(b"\x1b[B")
                form.expect_focus("Append output")
                draft_output = "~/kept.log"  # Fits the narrow input viewport.
                session.send(draft_output)
                session.send(b"\x1b[B")
                form.expect_focus("Separate error")
                session.send(b"\x1b[A\x1b[A\x1b[D")
                form.expect_focus("Task output")
                session.send(b"\x1b[B")
                form.expect_focus("Existing log")  # Inherit skips both redirect paths.
                session.send(b"\x1b[A\x1b[C\x1b[B")
                form.expect_focus("Append output")
                form.wait_value("Append output", draft_output)
                # Choosing each discard policy keeps file paths as an inactive
                # draft and does not move the focused selector.
                session.send(b"\x1b[A\x1b[C")
                policy_y = form.expect_focus("Task output")
                session.send(b"\x1b[C")
                assert form.expect_focus("Task output") == policy_y
                session.send(b"\x1b[B")
                form.expect_focus("Existing log")
                session.send(b"\x1b[B")
                form.expect_focus("Host")
                session.send(b"\x1b"); session.pump(0.2); session.close()
            assert cron.read_bytes() == before and not Path(env["FIXTURE_WRITES"]).exists()
            assert not Path(env["FIXTURE_PUEUE_SUBMISSION"]).exists()

    with tempfile.TemporaryDirectory(prefix="lazycrontab-stable-", dir="/tmp") as tmp:
        root = Path(tmp)
        env, args, cron = prepare_fixture(root)
        install_pueue_fixture(root, env)
        old_output, new_output = str(root / "old.log"), str(root / "new.log")
        subprocess.run([binary, *args, "add", "--name", "Output fixture", "--schedule", "* * * * *", "--command", "echo output", "--runner", "pueue", "--output", old_output, "--yes", "--json"], env=env, cwd=tmp, check=True, capture_output=True)
        before = cron.read_bytes()
        with terminal(binary, args, env, root, 120, 54) as (session, form):
            session.expect("First task")
            session.send("/Output fixture\r"); session.pump(0.2)
            session.send("e"); session.expect("edit job")
            session.send(b"\x0f")
            form.expect_focus("Run directly or enqueue")
            session.send(b"\x1b[B" * 5)
            form.expect_focus("Append output")
            session.send(b"\x01\x0b")
            form.expect_focus("Append output")
            form.wait_value("Append output", "", exact=True)
            session.send(new_output)
            form.expect_focus("Append output")
            form.wait_value("Append output", new_output)
            session.send(b"\x01\x0b\x1b[B")
            form.expect_focus("Separate error")
            session.send(b"\x1b[A")
            form.expect_focus("Append output")  # File policy keeps empty paths editable.
            session.send(new_output)
            session.send(b"\x1b[A\x1b[D\x1b[B")
            form.expect_focus("Existing log")
            session.send(b"\x1b[A\x1b[C\x1b[B")
            form.expect_focus("Append output")
            form.wait_value("Append output", new_output)
            mark = session.mark(); session.send(b"\x1bOP")
            session.expect("Output and cron mail", mark)
            mark = session.mark(); session.send(b"\x1b")
            session.expect("edit job", mark)
            form.expect_focus("Append output")
            form.wait_value("Append output", new_output)
            session.send(b"\x1b"); session.pump(0.2); session.close()
        assert cron.read_bytes() == before and not Path(env["FIXTURE_PUEUE_SUBMISSION"]).exists()

        # Invalid values in script-only fields remain a draft for that preset;
        # selecting a shell command must not parse or compile those inactive fields.
        with terminal(binary, args, env, root, 120, 54) as (session, form):
            session.expect("First task")
            session.send("n"); session.expect("add job")
            session.send("\t\tInactive values\t\techo valid")
            session.send(b"\x1b[A" + b"\x1b[C" * 4 + b"\x1b[B")
            form.expect_focus("Existing script")
            session.send("missing.py\tnot-a-runtime\t/not/a/project\t\t'")
            form.expect_focus("Arguments")
            session.send(b"\x1b[A" * 5 + b"\x1b[D" * 4 + b"\x1b[B")
            form.expect_focus("Command to execute")
            mark = session.mark(); session.send(b"\x13")
            session.expect("Task: Shell command", mark)
            session.expect("echo valid", mark)
            session.send(b"\x1b"); session.pump(0.2)
            session.send(b"\x1b"); session.pump(0.2); session.close()
        assert cron.read_bytes() == before
    print("Stable-form PTY passed: real-cell geometry at three sizes, arrow navigation/selectors, fixed payload/inactive slots, anchored expansion, stable direct/Pueue/output-policy rows, disabled keyboard/mouse skipping, file routing clear/refill and retained drafts, contextual output help, inactive invalid draft values, no job execution, terminal restoration")


if __name__ == "__main__":
    main()
