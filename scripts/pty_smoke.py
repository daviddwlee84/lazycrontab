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

    def click(self, x, y):
        """Click a zero-based terminal cell using real SGR press/release bytes."""
        self.send(f"\x1b[<0;{x+1};{y+1}M\x1b[<0;{x+1};{y+1}m")

    def wheel(self, x, y, down=True):
        self.send(f"\x1b[<{65 if down else 64};{x+1};{y+1}M")

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
            rows=subprocess.check_output(["ps","-axo","pid,ppid,pgid,stat,comm"],text=True).splitlines()[1:]
            tree=[];owned={self.process.pid}
            for _ in range(5):
                for row in rows:
                    parts=row.strip().split(None,4)
                    if len(parts)==5 and (int(parts[0]) in owned or int(parts[1]) in owned):
                        owned.add(int(parts[0]))
                        if row not in tree:tree.append(row)
            self.process.terminate()
            self.process.wait(timeout=5)
            raise AssertionError(f"TUI failed to exit; process tree={tree}; output={self.output[-1500:]!r}")
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
                    "FIXTURE_CRON": str(cron), "FIXTURE_PUEUE": str(root/"pueue-args"),
                    "FIXTURE_EXECUTED": str(root/"script-executed")})
        for key in ("LAZYCRONTAB_CONFIG", "LAZYCRONTAB_HOST", "LAZYCRONTAB_SOURCE", "NO_COLOR"):
            env.pop(key, None)
        for category in ("CONFIG", "DATA", "STATE", "CACHE"):
            env[f"XDG_{category}_HOME"] = str(root/category.lower())
        config = root/"config.toml"
        config.write_text('timezone="UTC"\nrefresh_seconds=60\n[[hosts]]\nid="lab"\nssh="fixture-host"\ntimezone="UTC"\n')
        script = root/"job.sh"
        script.write_text('#!/bin/sh\nprintf ran > "${FIXTURE_EXECUTED:?}"\necho fixture\n')
        script.chmod(0o750)
        (root/".ssh").mkdir()
        (root/".ssh"/"config").write_text("Host fixture-host\n  HostName 127.0.0.1\nHost pick-me\n  HostName 127.0.0.2\nHost noisy-*\n  User nobody\n")
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

            # A true Playground tab can be left directly. Typing owns numbers;
            # Esc leaves the field before ordinary page shortcuts are active.
            mark=session.mark();session.send("3")
            session.expect("F1 Fields",mark)
            mark=session.mark();session.send("1")
            session.expect("Seed task",mark)
            mark=session.mark();session.send("3")
            session.expect("F1 Fields",mark)
            session.send(b"\r\x01\x0b12")  # edit minute, Home, delete to end
            session.pump(0.3)
            session.expect("12 9 * * 1-5",mark)
            mark=session.mark();session.send(b"\x1b1");session.pump(0.2)
            assert b"Seed task" not in session.output[mark:], "Alt+1 still changed the page"
            session.send(b"\x1b");session.pump(0.2)
            mark=session.mark();session.send("1")
            session.expect("Seed task",mark)
            mark=session.mark();session.send("3")
            session.expect("12 9 * * 1-5",mark)
            # Use opens the same reviewed add workflow; cancelling returns
            # to the still-intact Playground instead of a new child process.
            mark=session.mark();session.send("u")
            session.expect("add job",mark)
            session.send(b"\t"*6)  # Scroll to Schedule past reserved script rows.
            session.expect("12 9 * * 1-5",mark)
            before=cron.read_text()
            mark=session.mark();session.send(b"\x1b")
            session.pump(0.2);session.resize(121,32)
            session.expect("12 9 * * 1-5",mark)
            session.resize(120,32);session.pump(0.2)
            assert cron.read_text()==before,"Playground draft cancellation changed cron"
            # The header tabs are actual mouse targets, including while editing.
            mark=session.mark();session.click(17,0)
            session.expect("Seed task",mark)
            mark=session.mark();session.click(42,0)
            session.expect("12 9 * * 1-5",mark)
            session.send("1");session.pump()

            mark=session.mark();session.send("n")
            session.expect("add job",mark)
            session.send("\t\tPTY backup\t\tprintf pty-value\t\t\t\tfixture remark")
            mark=session.mark();session.send(b"\x1bOP")  # F1 concepts inside the draft
            session.expect("Concepts",mark)
            mark=session.mark();session.send(b"\x1b")
            session.expect("add job",mark)
            session.expect("fixture remark",mark)
            # Click Review, Back and Apply through shared semantic buttons.
            mark=session.mark();session.click(10,28)
            session.expect("proposed",mark)
            before=cron.read_text()
            session.send(b"\r")
            session.pump()
            assert cron.read_text()==before, "Enter applied a default-No review"
            # At 120x32 the embedded review panel is centered beneath the
            # dashboard header; Back/Apply/Close live inside that panel.
            session.click(10,28)
            session.pump()
            mark=session.mark();session.click(10,28)
            session.expect("proposed",mark)
            mark=session.mark();session.click(24,28)
            session.expect("Saved",mark)
            assert "PTY backup" in cron.read_text()
            assert "fixture remark" in cron.read_text(), "typed remark was lost"
            assert '"id":"seed"' in cron.read_text(), "Add replaced the selected existing job"
            mark=session.mark();session.click(10,28)
            session.expect("PTY backup",mark)
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
            session.expect("Saved",mark)
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
            session.expect("F1 Fields",mark)
            session.send("1");session.pump(0.5)
            mark=session.mark();session.send("?")
            session.expect("Contextual actions",mark)
            # A wheel event in help cannot move the underlying selected job.
            session.wheel(30,8);session.wheel(30,8);session.pump()
            session.send(b"\x1b");session.pump()
            mark=session.mark();session.send("e")
            session.expect("edit job",mark)
            session.expect("Seed task",mark)
            session.send(b"\x1b");session.pump()
            # Host discovery reads only the private ssh config and selection is
            # still merely a draft until its explicit registration review.
            before=config.read_text();mark=session.mark();session.send("a")
            session.expect("pick-me",mark)
            session.send("/pick-me");session.send(b"\r");session.send(" ")
            session.expect("Selected: pick-me",mark)
            session.send(b"\x1b");session.pump()
            assert config.read_text()==before,"host picker cancellation registered a host"
            session.close()
        except Exception:
            os.killpg(session.process.pid, signal.SIGKILL)
            session.process.wait()
            raise

        # Standalone forms use the same script chooser and schedule component.
        # Preflight reads files and runtime paths but never executes this script.
        before=cron.read_text()
        session=Session(binary,[*base,"add","--interactive","--name","PTY shell",
                                "--preset","shell","--script",str(script),"--runtime","/bin/sh"],env,tmp,120,40)
        try:
            session.expect("Existing script file on selected host")
            mark=session.mark();session.click(110,10)
            session.expect("Choose Existing script file on selected host",mark)
            session.send("job.sh");session.pump(0.4)
            mark=session.mark();session.send(b"\r")
            session.expect("Draft only",mark)
            # Move from Script through Runtime, Directory and Arguments to Schedule.
            mark=session.mark();session.send(b"\t\t\t\t\r")
            session.expect("F1 Fields",mark)
            session.pump(0.4)
            mark=session.mark();session.send("u")
            session.expect("add job",mark)
            mark=session.mark();session.send(b"\x13")
            session.expect("Task: Existing shell script",mark)
            session.expect("Execution checks",mark)
            session.send(b"\x1b[6~")
            session.expect("proposed",mark)
            assert cron.read_text()==before,"script preflight saved or ran the job"
            assert not (root/"script-executed").exists(),"script preflight executed the user's script"
            session.send(b"\x1b");session.pump()
            session.close(b"\x1b")
            assert cron.read_text()==before,"cancelled script form changed cron"
        except Exception:
            os.killpg(session.process.pid,signal.SIGKILL);session.process.wait();raise

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
        mark=session.mark();session.send("?");session.expect("Contextual actions",mark)
        session.send(b"\x1b");session.pump()
        process_rows=subprocess.check_output(["ps","-axo","pid,ppid,comm"],text=True).splitlines()[1:]
        children=[int(parts[0]) for row in process_rows if len(parts:=row.strip().split(None,2))==3 and int(parts[1])==session.process.pid]
        assert len(children)==1, children
        os.kill(children[0],signal.SIGTERM)
        session.close(None)
        assert session.process.returncode==130, f"SIGTERM exited {session.process.returncode}, expected cancellation 130; output={session.output[-1800:]!r}"
        for marker in (root/"cache"/"lazycrontab"/"ssh").glob("*.json"):
            directory=Path(json.loads(marker.read_text())["directory"])
            assert str(directory).startswith(f"/tmp/lct-{os.getuid()}-")
            directory.rmdir()  # Fake SSH never creates a socket or master.
        assert not (root/"pueue-args").exists(),"smoke test submitted a Pueue job"
        print("PTY passed: text ownership, resize, Playground blur/page navigation and preserved draft, shared add/review/Back/apply without replacing existing jobs, delete cancel, SGR tabs/forms/script picker, script preset preflight and schedule editor, readable child errors, editor return, week/agenda, modal wheel containment, SSH alias picker/auth handoff, signal exit, terminal restoration")


if __name__ == "__main__":
    main()
