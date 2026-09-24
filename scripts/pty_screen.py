#!/usr/bin/env python3
"""Shared cell-grid assertions for incremental terminal rendering.

Only Python's standard library is required. The parser supports the VT operations
emitted by the PTY fixtures, not every terminal extension or grapheme sequence.
"""
import codecs
import re
import time
import unicodedata


class Screen:
    """The VT operations emitted by Bubble Tea, ignoring color and terminal queries.

    Reading the actual cell grid lets tests compare positions without resizing
    the app, which would otherwise hide the missing-layout-update regression.
    """
    def __init__(self, cols, rows):
        self.cols, self.rows = cols, rows
        self.cells = [[" "] * cols for _ in range(rows)]
        self.x = self.y = 0
        self.top, self.bottom = 0, rows - 1
        self.saved = (0, 0)
        self.pending = ""
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")

    def scroll(self, count):
        count = max(-self.rows, min(self.rows, count))
        for _ in range(abs(count)):
            if count > 0:
                self.cells.pop(self.top)
                self.cells.insert(self.bottom, [" "] * self.cols)
            else:
                self.cells.pop(self.bottom)
                self.cells.insert(self.top, [" "] * self.cols)

    def newline(self):
        if self.y == self.bottom:
            self.scroll(1)
        else:
            self.y = min(self.rows - 1, self.y + 1)

    def csi(self, raw, final):
        private = raw.startswith(("?", ">", "<", "="))
        raw = raw.lstrip("?><=")
        values = [int(value) if value.isdigit() else 0 for value in raw.split(";")]
        n = values[0] or 1
        if final in "mhl" and private:
            if final == "h" and 1049 in values:
                self.cells = [[" "] * self.cols for _ in range(self.rows)]
                self.x = self.y = 0
            return
        if final in ("H", "f"):
            self.y = min(self.rows - 1, max(0, n - 1))
            self.x = min(self.cols - 1, max(0, (values[1] if len(values) > 1 and values[1] else 1) - 1))
        elif final == "A": self.y = max(0, self.y - n)
        elif final in ("B", "e"): self.y = min(self.rows - 1, self.y + n)
        elif final in ("C", "a"): self.x = min(self.cols - 1, self.x + n)
        elif final == "D": self.x = max(0, self.x - n)
        elif final == "E": self.y, self.x = min(self.rows - 1, self.y + n), 0
        elif final == "F": self.y, self.x = max(0, self.y - n), 0
        elif final in ("G", "`"): self.x = min(self.cols - 1, n - 1)
        elif final == "d": self.y = min(self.rows - 1, n - 1)
        elif final == "r":
            self.top = min(self.rows - 1, n - 1)
            self.bottom = min(self.rows - 1, (values[1] if len(values) > 1 and values[1] else self.rows) - 1)
            self.x = self.y = 0
        elif final == "S": self.scroll(n)
        elif final == "T": self.scroll(-n)
        elif final in ("L", "M"):
            previous = self.top
            self.top = self.y
            self.scroll(-n if final == "L" else n)
            self.top = previous
        elif final in ("J", "K", "X"):
            mode = values[0]
            if final == "X":
                self.cells[self.y][self.x:min(self.cols, self.x + n)] = [" "] * max(0, min(self.cols, self.x + n) - self.x)
            elif final == "K":
                start, end = (0, self.cols) if mode == 2 else (0, min(self.cols, self.x + 1)) if mode == 1 else (self.x, self.cols)
                self.cells[self.y][start:end] = [" "] * max(0, end - start)
            elif mode in (2, 3):
                self.cells = [[" "] * self.cols for _ in range(self.rows)]
            elif mode == 0:
                self.cells[self.y][self.x:] = [" "] * max(0, self.cols - self.x)
                for row in range(self.y + 1, self.rows): self.cells[row] = [" "] * self.cols
            elif mode == 1:
                for row in range(self.y): self.cells[row] = [" "] * self.cols
                self.cells[self.y][:self.x + 1] = [" "] * min(self.cols, self.x + 1)
        elif final == "P":
            line = self.cells[self.y]
            self.cells[self.y] = (line[:self.x] + line[self.x + n:] + [" "] * n)[:self.cols]
        elif final == "@":
            line = self.cells[self.y]
            self.cells[self.y] = (line[:self.x] + [" "] * n + line[self.x:])[:self.cols]
        elif final == "s": self.saved = (self.x, self.y)
        elif final == "u" and not private: self.x, self.y = self.saved

    def feed(self, data):
        text = self.pending + self.decoder.decode(data)
        i = 0
        while i < len(text):
            char = text[i]
            if char == "\x1b":
                if i + 1 == len(text): break
                kind = text[i + 1]
                if kind == "[":
                    match = re.match(r"\x1b\[([0-?]*)([ -/]*)([@-~])", text[i:])
                    if not match: break
                    self.csi(match[1], match[3]); i += len(match[0]); continue
                if kind in ("]", "P", "_", "^"):
                    match = re.search(r"\x07|\x1b\\", text[i + 2:])
                    if not match: break
                    i += 2 + match.end(); continue
                if kind in "()*+":
                    if i + 2 >= len(text): break
                    i += 3; continue
                if kind == "7": self.saved = (self.x, self.y)
                elif kind == "8": self.x, self.y = self.saved
                elif kind == "D": self.newline()
                elif kind == "M":
                    if self.y == self.top: self.scroll(-1)
                    else: self.y = max(0, self.y - 1)
                i += 2; continue
            if char == "\r": self.x = 0
            elif char == "\n": self.newline()
            elif char == "\b": self.x = max(0, self.x - 1)
            elif char == "\t": self.x = min(self.cols - 1, (self.x // 8 + 1) * 8)
            elif ord(char) >= 32 and char != "\x7f":
                width = 0 if unicodedata.combining(char) else 2 if unicodedata.east_asian_width(char) in ("W", "F") else 1
                if width == 0:
                    if self.x: self.cells[self.y][min(self.cols - 1, self.x - 1)] += char
                else:
                    if self.x >= self.cols:
                        self.x = 0; self.newline()
                    self.cells[self.y][self.x] = char
                    if width == 2 and self.x + 1 < self.cols: self.cells[self.y][self.x + 1] = ""
                    self.x += width
            i += 1
        self.pending = text[i:]

    def lines(self):
        return ["".join(row) for row in self.cells]


class SessionScreen:
    """Observe current cells at a fixed size, including unchanged render spans."""
    def __init__(self, session, cols, rows, offset=0):
        self.session, self.screen, self.offset = session, Screen(cols, rows), offset

    def read(self):
        self.session.pump(0.05)
        self.screen.feed(self.session.output[self.offset:])
        self.offset = len(self.session.output)
        return self.screen.lines()

    def expect(self, *texts, absent=(), timeout=12):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            lines = self.read()
            if (all(any(text in line for line in lines) for text in texts)
                    and all(not any(text in line for line in lines) for text in absent)):
                return lines
            if self.session.process.poll() is not None:
                break
        raise AssertionError(
            f"Expected visible {texts!r}, absent {absent!r}; exit={self.session.process.poll()}\n"
            + "\n".join(self.screen.lines())
        )
