#!/usr/bin/env python3
"""Real PTY acceptance against private cron/SSH/editor fixtures, never live jobs.

Usage: python3 scripts/pty_smoke.py /absolute/path/to/lazycrontab
Only Python's standard library is required.
"""
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


class Session:
    def __init__(self, binary, args, env, cwd, cols=120, rows=32):
        self.master, self.slave = pty.openpty()
        self.before = termios.tcgetattr(self.slave)
        self.before_path=Path(cwd)/f"tty-before-{time.monotonic_ns()}"
        self.after_path=Path(cwd)/f"tty-after-{time.monotonic_ns()}"
        self.resize(cols, rows)
        def controlling_terminal():
            fcntl.ioctl(0, termios.TIOCSCTTY, 0)
        self.process = subprocess.Popen(
            ["/bin/sh", "-c", 'before=$1; after=$2; shift 2; stty -g > "$before"; "$@"; result=$?; stty -g > "$after"; exit "$result"',
             "pty-fixture", str(self.before_path), str(self.after_path), binary, *args], stdin=self.slave, stdout=self.slave,
            stderr=self.slave, cwd=cwd, env=env, start_new_session=True,
            preexec_fn=controlling_terminal,
        )
        self.output = b""

    def resize(self, cols, rows):
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def send(self, data):
        os.write(self.master, data.encode() if isinstance(data, str) else data)

    def pump(self, duration=0.15):
        until = time.monotonic() + duration
        while time.monotonic() < until:
            ready, _, _ = select.select([self.master], [], [], min(0.05, max(0, until-time.monotonic())))
            if not ready:
                continue
            data = os.read(self.master, 65536)
            self.output += data
            # Respond to terminal discovery without pretending to support Kitty.
            if b"\x1b[6n" in data:
                self.send(b"\x1b[1;1R")
            if b"\x1b]11;?" in data:
                self.send(b"\x1b]11;rgb:0000/0000/0000\x1b\\")
            if b"\x1b[c" in data:
                self.send(b"\x1b[?1;2c")

    def expect(self, text, since=0, timeout=12):
        needle = text.encode()
        until = time.monotonic() + timeout
        while time.monotonic() < until:
            if needle in self.output[since:]:
                return
            self.pump()
            if self.process.poll() is not None:
                break
        raise AssertionError(f"missing {text!r}; exit={self.process.poll()}\n{self.output[-9000:]!r}")

    def mark(self):
        self.pump()
        return len(self.output)

    def close(self, key=b"q"):
        if key is not None:
            self.send(key)
        until = time.monotonic() + 5
        while self.process.poll() is None and time.monotonic() < until:
            self.pump()
        if self.process.poll() is None:
            self.process.terminate()
            self.process.wait(timeout=5)
            raise AssertionError("TUI failed to exit")
        def persistent_modes(raw):
            # BSD may set PENDIN when switching buffered input back to canonical
            # mode. It is transient kernel state, not a persistent tty setting.
            mask=~getattr(termios,"PENDIN",0)
            if raw.startswith("gfmt1:"):
                return re.sub(r"lflag=([0-9a-f]+)",lambda m:f"lflag={int(m[1],16)&mask:x}",raw)
            parts=raw.strip().split(":")
            parts[3]=f"{int(parts[3],16)&mask:x}"
            return ":".join(parts)
        assert persistent_modes(self.after_path.read_text()) == persistent_modes(self.before_path.read_text()), "persistent terminal modes were not restored"
        os.close(self.master)
        os.close(self.slave)


def main():
    binary = str(Path(sys.argv[1] if len(sys.argv)>1 else "./lazycrontab").resolve())
    with tempfile.TemporaryDirectory(prefix="lazycrontab-pty-") as tmp:
        root = Path(tmp)
        bin_dir = root / "bin"
        bin_dir.mkdir()
        cron = root / "crontab"
        cron.write_text('# fixture header\nPATH=/usr/bin:/bin\n# lazycrontab: {"v":1,"id":"seed","name":"Seed task","remark":"專案 é 👩🏽‍💻"}\n0 9 * * * printf seed\n')
        (bin_dir / "crontab").write_text('''#!/bin/sh
case "$1" in
 -l) if [ -f "$FIXTURE_CRON" ]; then cat "$FIXTURE_CRON"; else echo 'no crontab for fixture' >&2; exit 1; fi;;
 -) cat > "$FIXTURE_CRON";;
 *) exit 2;;
esac
''')
        (bin_dir / "ssh").write_text('''#!/bin/sh
if [ "$1" = -G ]; then printf 'controlpath none\\n'; exit; fi
for last do :; done
if [ "$last" = true ]; then printf 'Fixture SSH authentication: '; read answer; [ "$answer" = yes ]; exit; fi
sleep 0.4
exec sh -c "$last"
''')
        (bin_dir / "pueue").write_text('''#!/bin/sh
case "$1" in
 --version) echo 'pueue 4.0.2';;
 group) echo '{"default":{"status":"Running","parallel_tasks":1},"backup":{"status":"Paused","parallel_tasks":1}}';;
 add) printf '%s\\n' "$@" > "$FIXTURE_PUEUE"; echo 42;;
 *) exit 2;;
esac
''')
        (bin_dir / "fixture-editor").write_text('''#!/bin/sh
for last do :; done
printf '\\n# edited by PTY fixture\\n' >> "$last"
''')
        for file in bin_dir.iterdir():
            file.chmod(0o700)
        env = dict(os.environ)
        env.update({"HOME": tmp, "TERM": "xterm-256color", "PATH": str(bin_dir)+":/usr/bin:/bin",
                    "VISUAL": str(bin_dir/"fixture-editor"), "EDITOR": str(bin_dir/"fixture-editor"),
                    "FIXTURE_CRON": str(cron), "FIXTURE_PUEUE": str(root/"pueue-args")})
        for key in ("LAZYCRONTAB_CONFIG", "LAZYCRONTAB_HOST", "LAZYCRONTAB_SOURCE", "NO_COLOR"):
            env.pop(key, None)
        for category in ("CONFIG", "DATA", "STATE", "CACHE"):
            env[f"XDG_{category}_HOME"] = str(root/category.lower())
        config = root/"config.toml"
        config.write_text('timezone="UTC"\nrefresh_seconds=60\n[[hosts]]\nid="lab"\nssh="fixture-host"\ntimezone="UTC"\n')
        script = root/"job.sh"
        script.write_text("#!/bin/sh\necho fixture\n")
        script.chmod(0o750)
        base = ["--config", str(config)]
        subprocess.run([binary,*base,"edit","seed","--script",str(script),"--yes"],env=env,cwd=tmp,check=True,capture_output=True)

        session = Session(binary, base, env, tmp)
        try:
            session.expect("Seed task")
            session.send("/jkhql/?")
            session.pump()
            assert session.process.poll() is None, "input mnemonics quit the dashboard"
            session.send(b"\x1b")
            session.pump()
            for size in ((80,24),(40,12),(120,32)):
                session.resize(*size)
                session.pump()
            mark=session.mark();session.send("n")
            session.expect("add job",mark)
            session.send("\t\tPTY backup\tprintf pty-value\tfixture remark\t")
            session.send(b"\x1b[C")  # Daily -> Weekdays; no raw cron memorization.
            mark=session.mark();session.send(b"\x13")
            session.expect("proposed",mark)
            before=cron.read_text()
            session.send(b"\r")
            session.pump()
            assert cron.read_text()==before, "Enter applied a default-No review"
            session.send(b"\x1b")
            session.pump()
            mark=session.mark();session.send(b"\x13")
            session.expect("proposed",mark)
            mark=session.mark();session.send(b"\x13")
            session.expect("saved",mark)
            assert "PTY backup" in cron.read_text()
            mark=session.mark();session.send(b"\r")
            session.expect("lazycrontab",mark)
            session.pump(0.5)

            before=cron.read_text();mark=session.mark();session.send("d")
            session.expect("remove",mark)
            session.send(b"\r");session.pump()
            assert cron.read_text()==before
            session.send(b"\x1b");session.pump(0.5)
            assert cron.read_text()==before, "cancel deleted a job"

            # Actual SGR mouse input selects the second job. Its missing script
            # path must remain readable before returning to the dashboard.
            mark=session.mark()
            session.send(b"\x1b[<0;31;6M\x1b[<0;31;6m")
            session.send("E")
            session.expect("set --path",mark)
            session.expect("Press Enter to return",mark)
            session.send(b"\r");session.pump(0.5)
            session.send("k");session.pump()

            mark=session.mark();session.send("E")
            session.expect("Save script",mark)
            mark=session.mark();session.send("y")
            session.expect("saved",mark)
            assert "edited by PTY" in script.read_text()
            assert script.stat().st_mode & 0o777 == 0o750
            session.send(b"\r");session.pump(0.5)

            mark=session.mark();session.send("2")
            session.expect("Forecast",mark)
            session.pump(0.8)
            mark=session.mark();session.send(b"\r")
            session.expect("Agenda",mark)
            session.send(b"\x1b");session.send("1");session.pump()
            mark=session.mark();session.send("3")
            session.expect("playground",mark)
            session.send(b"\x1b");session.pump(0.5)
            mark=session.mark();session.send("?")
            session.expect("Contextual actions",mark)
            session.send(b"\x1b");session.pump()
            session.close()
        except Exception:
            os.killpg(session.process.pid, signal.SIGKILL)
            session.process.wait()
            raise

        # A bare command with global target flags must still open its wizard.
        session=Session(binary,[*base,"--host","lab","add"],env,tmp,80,24)
        session.expect("add job")
        session.close(b"\x1b")

        # Native SSH authentication temporarily owns the terminal.
        session=Session(binary,[*base,"--host","lab"],env,tmp,100,28)
        session.expect("Seed task")
        mark=session.mark();session.send("A")
        session.expect("Fixture SSH authentication",mark)
        session.send("yes\n");session.pump(0.8)
        assert session.process.poll() is None
        session.close()
        session=Session(binary,base,env,tmp,80,24)
        session.expect("Seed task")
        process_rows=subprocess.check_output(["ps","-axo","pid,ppid,comm"],text=True).splitlines()[1:]
        children=[int(parts[0]) for row in process_rows if len(parts:=row.strip().split(None,2))==3 and int(parts[1])==session.process.pid]
        assert len(children)==1, children
        os.kill(children[0],signal.SIGTERM)
        session.close(None)
        assert session.process.returncode==130
        for marker in (root/"cache"/"lazycrontab"/"ssh").glob("*.json"):
            directory=Path(json.loads(marker.read_text())["directory"])
            assert str(directory).startswith(f"/tmp/lct-{os.getuid()}-")
            directory.rmdir()  # Fake SSH never creates a socket or master.
        print("PTY passed: input, resize, add/review/Back/apply, delete cancel, SGR mouse, readable child errors, editor return, week/agenda, playground, help, SSH handoff, signal exit, terminal restoration")


if __name__ == "__main__":
    main()
