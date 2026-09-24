#!/usr/bin/env python3
"""Managed-script acceptance with a real terminal and isolated fixture backends.

Usage: python3 scripts/pty_managed.py /absolute/path/to/lazycrontab
Uses only the Python standard library and never executes scheduled commands.
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
    with tempfile.TemporaryDirectory(prefix="lazycrontab-managed-pty-") as tmp:
        root = Path(tmp)
        bin_dir = root / "bin"
        bin_dir.mkdir()
        cron = root / "crontab"
        cron.write_text("# managed-script PTY fixture\n")
        original_cron = cron.read_bytes()
        fixtures = {
            "crontab": '''#!/bin/sh
case "$1" in
 -l) cat "$FIXTURE_CRON";;
 -) cat > "$FIXTURE_CRON";;
 *) exit 2;;
esac
''',
            "pueue": '''#!/bin/sh
case "$1" in
 --version) echo 'pueue 4.0.2';;
 group) echo '{"default":{"status":"Running","parallel_tasks":1}}';;
 add) printf '%s\\n' "$@" > "$FIXTURE_PUEUE"; exit 99;;
 *) exit 2;;
esac
''',
            "fixture-editor": '''#!/bin/sh
for last do :; done
stty -a > "$FIXTURE_EDITOR_TTY"
printf '%s' "$last" > "$FIXTURE_EDITOR_DRAFT"
cat "$FIXTURE_EDITOR_CONTENT" > "$last"
echo 'FIXTURE EDITOR HANDOFF'
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
            "FIXTURE_CRON": str(cron), "FIXTURE_PUEUE": str(root / "pueue-add"),
            "FIXTURE_EDITOR_CONTENT": str(root / "editor-content.sh"),
            "FIXTURE_EDITOR_DRAFT": str(root / "editor-draft-path"),
            "FIXTURE_EDITOR_TTY": str(root / "editor-tty"),
        })
        for key in ("LAZYCRONTAB_CONFIG", "LAZYCRONTAB_HOST", "LAZYCRONTAB_SOURCE", "NO_COLOR"):
            env.pop(key, None)
        for category in ("CONFIG", "DATA", "STATE", "CACHE"):
            env[f"XDG_{category}_HOME"] = str(root / category.lower())
        config = root / "config.toml"
        config.write_text('timezone="UTC"\nrefresh_seconds=60\n')
        base = ["--config", str(config)]
        data = root / "data" / "lazycrontab"

        def scripts():
            return {path: path.read_bytes() for path in (data / "scripts").glob("*/*.sh")}

        def recipe():
            paths = list((data / "jobs").glob("*.json"))
            assert len(paths) == 1, paths
            return json.loads(paths[0].read_text())

        def run(*args):
            return subprocess.run([binary, *base, *args], env=env, cwd=tmp, check=True, capture_output=True, text=True).stdout

        def enter_content(session, tabs):
            mark = session.mark()
            session.send(b"\t" * tabs + b"\r")
            session.expect("saved after job review and Apply", mark)

        def leave_content(session):
            mark = session.mark()
            session.send(b"\x13")
            session.expect("Ctrl+S reviews before saving", mark)

        def review(session):
            mark = session.mark()
            session.send(b"\x13")
            session.expect("Managed script content", mark)
            session.expect("Enter does not apply", mark)

        def apply(session):
            mark = session.mark()
            session.send("y")
            session.expect("Saved", mark)

        def editor_handoff(session, content):
            Path(env["FIXTURE_EDITOR_CONTENT"]).write_bytes(content)
            mark = session.mark()
            session.send(b"\x1bOS")  # F4: release the owning TUI for its editor.
            session.expect("FIXTURE EDITOR HANDOFF", mark)
            session.expect("Use F4 editor to preserve", mark)
            tty_modes = Path(env["FIXTURE_EDITOR_TTY"]).read_text()
            assert re.search(r"(?:^|[;\s])icanon(?:[;\s]|$)", tty_modes), tty_modes
            assert re.search(r"(?:^|[;\s])echo(?:[;\s]|$)", tty_modes), tty_modes
            draft = Path(Path(env["FIXTURE_EDITOR_DRAFT"]).read_text())
            assert not draft.exists(), "external-editor private draft was not cleaned"

        add = [*base, "add", "--interactive", "--preset", "managed-shell", "--name", "Managed PTY",
               "--schedule", "* * * * *", "--runner", "pueue", "--group", "default", "--runtime", "/bin/sh"]
        body = b'echo "hi"\nprintf \'value=%s\\n\' \'100%\'\n'
        for save in (False, True):
            with terminal(binary, add, env, root) as session:
                session.expect("Managed shell script")
                enter_content(session, 4)  # host, source, name, task type, content
                session.send(b"\x1b[200~" + body + b"\x1b[201~")
                session.pump(0.3)
                leave_content(session)
                assert not scripts() and cron.read_bytes() == original_cron, "Ctrl+S from content saved a job"
                review(session)
                assert not scripts() and cron.read_bytes() == original_cron, "review published a script"
                session.send(b"\r")
                session.pump(0.2)
                assert not scripts(), "Enter approved the default-No review"
                if save:
                    apply(session)
                    session.close(b"\r")
                else:
                    session.send(b"\x1b")
                    session.pump(0.2)
                    session.close(b"\x1b")
                    assert not scripts() and cron.read_bytes() == original_cron, "cancel saved a script"

        first = Path(recipe()["script"])
        assert first.read_bytes() == body, first.read_bytes()
        assert first.stat().st_mode & 0o777 == 0o600, "managed script is not private"
        assert str(first) in cron.read_text() and "pueue" in cron.read_text(), cron.read_text()
        metadata = next(line for line in cron.read_text().splitlines() if line.startswith("# lazycrontab:"))
        job_id = json.loads(metadata.split(":", 1)[1])["id"]

        # A nested dashboard form releases and regains terminal ownership for
        # F4; tabs survive the editor handoff and cancelling publishes nothing.
        editor_body = b'echo "F4 edited"\n\tprintf \'%s\\n\' \'100%\'\n'
        before_cron, before_scripts = cron.read_bytes(), scripts()
        with terminal(binary, base, env, root) as session:
            session.expect("Managed PTY")
            mark = session.mark()
            session.send("e")
            session.expect("edit job", mark)
            enter_content(session, 2)
            editor_handoff(session, editor_body)
            leave_content(session)
            review(session)
            session.send(b"\x1b")
            session.pump(0.2)
            mark = session.mark()
            session.send(b"\x1b")
            session.expect("Managed PTY", mark)
            session.close()
        assert cron.read_bytes() == before_cron and scripts() == before_scripts, "cancelled nested editor saved changes"

        with terminal(binary, [*base, "edit", job_id, "--interactive"], env, root) as session:
            session.expect("edit job")
            enter_content(session, 2)
            editor_handoff(session, editor_body)
            leave_content(session)
            assert cron.read_bytes() == before_cron and scripts() == before_scripts, "editor handoff wrote target"
            review(session)
            apply(session)
            session.close(b"\r")
        second = Path(recipe()["script"])
        assert second != first and second.read_bytes() == editor_body, "F4 changed tabs or reused an immutable path"
        assert first.read_bytes() == body and len(scripts()) == 2
        assert str(second) in cron.read_text() and str(first) not in cron.read_text()

        # The script command also creates a new immutable version, even with
        # an explicit --path selecting the currently managed file.
        third_body = b'echo "script edit"\n\tprintf \'literal %%\\n\'\n'
        Path(env["FIXTURE_EDITOR_CONTENT"]).write_bytes(third_body)
        before_cron, before_scripts = cron.read_bytes(), scripts()
        for save in (False, True):
            args = [*base, "script", "edit", job_id]
            if not save:
                args += ["--path", str(second)]
            with terminal(binary, args, env, root) as session:
                session.expect("Save managed script")
                session.send(b"\x1b[6~")
                session.expect("Script content changes")
                assert cron.read_bytes() == before_cron and scripts() == before_scripts, "script editor wrote before review"
                if save:
                    apply(session)
                    session.close(b"\r")
                else:
                    session.close(b"\x1b")
                    assert cron.read_bytes() == before_cron and scripts() == before_scripts, "cancelled script edit wrote target"
        third = Path(recipe()["script"])
        assert third not in (first, second) and third.read_bytes() == third_body
        assert first.read_bytes() == body and second.read_bytes() == editor_body and len(scripts()) == 3
        assert str(third) in cron.read_text() and str(second) not in cron.read_text()

        # File-based CLI input and script edit discovery remain read-only.
        before_cron, before_scripts = cron.read_bytes(), scripts()
        planned = json.loads(run("add", "--schedule", "* * * * *", "--script-content-file", env["FIXTURE_EDITOR_CONTENT"], "--runner", "pueue", "--dry-run", "--json"))
        assert planned["managed_script"]["content"] == third_body.decode()
        preview = json.loads(run("script", "edit", job_id, "--dry-run", "--json"))
        assert preview["path"] == str(third)
        assert cron.read_bytes() == before_cron and scripts() == before_scripts, "dry-run changed source/scripts"
        assert not Path(env["FIXTURE_PUEUE"]).exists(), "PTY regression submitted a Pueue task"
        print("Managed PTY passed: multiline paste and percent bytes, draft/review cancellation, private version publication, Pueue compilation without submission, nested F4 terminal handoff and exact tabs, immutable job/script edit versions, dry-run, terminal restoration")


if __name__ == "__main__":
    main()
