#!/usr/bin/env python3
"""Bounded keyboard-only M2 PTY acceptance using disposable synthetic HOMEs.

Usage: python3 tests/m2_pty.py /tmp/quicknote-m2 [--baseline]
Only stdlib is required (Linux/Unix PTYs). Commands are selected by their
registry-generated labels, not guessed future lifecycle shortcuts. Failures
never print screens, subprocess output or note payloads. --baseline checks the
pre-M2 terminal harness; it is not a passing M2 acceptance run.
"""

import codecs
import datetime as dt
import errno
import fcntl
import json
import os
import pty
import re
import select
import signal
import struct
import subprocess
import termios
import time
import unicodedata

from m2_cli import Home, CheckFailure, MissingFeature, arguments, check, parse_instant, run_suite, utcstamp


ESC, ENTER, TAB, DOWN = b"\x1b", b"\r", b"\t", b"\x1b[B"


class Screen:
    """Small VT screen sufficient for Bubble Tea's cursor/erase redraw protocol.

    The parser retains fragmented CSI/OSC/UTF-8 across reads. Query replies go
    straight to the PTY; output text is kept only in memory. Colors are ignored.
    This is a correctness harness, not a timing/terminal compatibility benchmark.
    """

    def __init__(self, columns, rows, reply):
        self.reply = reply
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")
        self.pending = ""
        self.saved = (0, 0)
        self.resize(columns, rows)

    def resize(self, columns, rows):
        self.columns, self.rows = columns, rows
        self.cells = [[" "] * columns for _ in range(rows)]
        self.x = self.y = 0

    @property
    def text(self):
        return "\n".join("".join(row).rstrip() for row in self.cells)

    def feed(self, data):
        self.pending += self.decoder.decode(data)
        while self.pending:
            if self.pending.startswith("\x1b["):
                end = next((i for i in range(2, len(self.pending))
                            if "@" <= self.pending[i] <= "~"), None)
                if end is None:
                    return
                args, code = self.pending[2:end], self.pending[end]
                self.pending = self.pending[end + 1:]
                self.csi(args, code)
                continue
            if self.pending.startswith("\x1b]"):
                match = re.search("\x07|\x1b\\\\", self.pending[2:])
                if match is None:
                    return
                end = match.start() + 2
                command = self.pending[2:end]
                self.pending = self.pending[end + len(match.group()):]
                if command == "11;?":
                    self.reply(b"\x1b]11;rgb:1e1e/1e1e/2e2e\x07")
                elif command == "10;?":
                    self.reply(b"\x1b]10;rgb:eeee/eeee/eeee\x07")
                continue
            if self.pending[0] == "\x1b":
                if len(self.pending) < 2:
                    return
                if self.pending[1] in "()":
                    if len(self.pending) < 3:
                        return
                    self.pending = self.pending[3:]
                    continue
                code = self.pending[1]
                self.pending = self.pending[2:]
                if code == "7":
                    self.saved = (self.x, self.y)
                elif code == "8":
                    self.x, self.y = self.saved
                continue
            char, self.pending = self.pending[0], self.pending[1:]
            if char == "\r":
                self.x = 0
            elif char == "\n":
                self.y += 1
                if self.y >= self.rows:
                    self.cells.pop(0)
                    self.cells.append([" "] * self.columns)
                    self.y = self.rows - 1
            elif char == "\b":
                self.x = max(0, self.x - 1)
            elif char >= " " and char != "\x7f":
                width = 0 if unicodedata.combining(char) else (2 if unicodedata.east_asian_width(char) in "WF" else 1)
                if width == 0:
                    if self.x:
                        self.cells[self.y][min(self.x - 1, self.columns - 1)] += char
                    continue
                if self.x >= self.columns:
                    self.x = 0
                    self.y = min(self.rows - 1, self.y + 1)
                self.cells[self.y][self.x] = char
                if width == 2 and self.x + 1 < self.columns:
                    self.cells[self.y][self.x + 1] = ""
                self.x += width

    def csi(self, args, code):
        if code == "n" and args == "6":
            self.reply(f"\x1b[{self.y + 1};{min(self.x + 1, self.columns)}R".encode())
            return
        if code in "hl" and args in ("?1049", "?1047"):
            self.resize(self.columns, self.rows)
            return
        if code == "c":
            self.reply(b"\x1b[?1;2c")
            return
        if args.startswith("?"):
            return
        parts = args.split(";")
        values = [int(p) if p.isdigit() else 0 for p in parts]
        n = values[0] or 1
        if code in "Hf":
            self.y = min(self.rows - 1, max(0, (values[0] or 1) - 1))
            self.x = min(self.columns - 1, max(0, ((values[1] if len(values) > 1 else 1) or 1) - 1))
        elif code == "A":
            self.y = max(0, self.y - n)
        elif code == "B":
            self.y = min(self.rows - 1, self.y + n)
        elif code == "C":
            self.x = min(self.columns - 1, self.x + n)
        elif code == "D":
            self.x = max(0, self.x - n)
        elif code == "G":
            self.x = min(self.columns - 1, n - 1)
        elif code == "d":
            self.y = min(self.rows - 1, n - 1)
        elif code == "E":
            self.y, self.x = min(self.rows - 1, self.y + n), 0
        elif code == "F":
            self.y, self.x = max(0, self.y - n), 0
        elif code == "J":
            if values[0] in (2, 3):
                self.cells = [[" "] * self.columns for _ in range(self.rows)]
            elif values[0] == 0:
                self.cells[self.y][min(self.x, self.columns):] = [" "] * max(0, self.columns - self.x)
                for y in range(self.y + 1, self.rows):
                    self.cells[y] = [" "] * self.columns
        elif code == "K":
            start, end = (0, self.columns) if values[0] == 2 else ((0, min(self.x + 1, self.columns)) if values[0] == 1 else (min(self.x, self.columns), self.columns))
            self.cells[self.y][start:end] = [" "] * (end - start)
        elif code == "s":
            self.saved = (self.x, self.y)
        elif code == "u":
            self.x, self.y = self.saved


class Terminal:
    def __init__(self, home, profile="default", columns=120, rows=40):
        self.home, self.profile = home, profile
        keyboard = home.path / ".config/quicknote-tui/keyboard.json"
        keyboard.parent.mkdir(parents=True, exist_ok=True)
        keyboard.write_text(json.dumps({"version": 1, "profile": profile}))
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))

        def controlling_tty():
            os.setsid()
            fcntl.ioctl(0, termios.TIOCSCTTY, 0)

        self.process = subprocess.Popen([home.binary], stdin=slave, stdout=slave,
                                        stderr=slave, env=home.env, cwd=home.path,
                                        preexec_fn=controlling_tty)
        os.close(slave)
        self.screen = Screen(columns, rows, self.reply)
        self.deadline = time.monotonic() + 100

    def __enter__(self):
        try:
            self.wait(lambda: bool(self.screen.text.strip()), "initial rendered screen", timeout=8)
            self.pump(0.3)
            return self
        except BaseException:
            self.__exit__()
            raise

    def __exit__(self, *_):
        if self.process.poll() is None:
            self.process.kill()
        self.process.wait(timeout=5)
        os.close(self.master)

    def reply(self, data):
        os.write(self.master, data)

    def pump(self, seconds=0.06):
        end = min(time.monotonic() + seconds, self.deadline)
        check(time.monotonic() < self.deadline, "PTY scenario exceeded 100-second timeout")
        while time.monotonic() < end:
            if not select.select([self.master], [], [], max(0, min(0.04, end - time.monotonic())))[0]:
                continue
            try:
                data = os.read(self.master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    break
                raise
            if not data:
                break
            self.screen.feed(data)
        check(self.process.poll() in (None, 0), "TUI exited with a nonzero status (output suppressed)")

    def wait(self, predicate, label, timeout=6):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            if predicate():
                return
            check(self.process.poll() is None, "TUI exited before " + label)
            self.pump(0.08)
        raise CheckFailure("timed out waiting for " + label + " (screen suppressed)")

    def key(self, data):
        check(self.process.poll() is None, "cannot send keys after TUI exit")
        os.write(self.master, data)
        self.pump()

    def paste(self, text):
        self.key(b"\x1b[200~" + text.encode() + b"\x1b[201~")

    def resize(self, columns, rows):
        self.screen.resize(columns, rows)
        fcntl.ioctl(self.master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))
        os.kill(self.process.pid, signal.SIGWINCH)
        self.pump(0.2)

    def four_sizes(self):
        for columns, rows in [(120, 40), (80, 24), (50, 12), (25, 5)]:
            self.resize(columns, rows)
            check(self.process.poll() is None, "resize exited the TUI")
            check("Esc" in self.screen.text or "Cancel" in self.screen.text,
                  "lifecycle panel lost its keyboard cancellation hint")
        self.resize(120, 40)

    def selected(self):
        return next((line[line.index(">") + 1:].strip() for line in self.screen.text.splitlines()
                     if re.search(r"(?:^|[│┃ ]|\s)> ", line)), "")

    def select(self, pattern, label):
        # Menus scroll with selection; reaching the end twice terminates search.
        previous = None
        for _ in range(90):
            selected = self.selected()
            if re.search(pattern, selected, re.I):
                if "Planned in QN-" in selected:
                    raise MissingFeature(label + " is still disabled by the action registry")
                return
            if selected and selected == previous:
                raise MissingFeature(label + " is absent from the keyboard menu")
            previous = selected
            self.key(DOWN)
        raise CheckFailure("could not identify a selected row for " + label)

    def action(self, pattern, label):
        self.key(b"\x1bp" if self.profile == "legacy" else b"\x0b")
        self.wait(lambda: "Commands" in self.screen.text, "Commands menu")
        self.select(pattern, label)
        self.key(ENTER)

    def ensure_editor(self):
        # Identify context by registry action availability, not note body/footer
        # text containing "Trash". Escape closes only the Commands layer.
        self.key(b"\x1bp" if self.profile == "legacy" else b"\x0b")
        self.wait(lambda: "Commands" in self.screen.text, "context Commands menu")
        previous = None
        for _ in range(90):
            selected = self.selected()
            if re.match(r"Return to notes", selected, re.I):
                self.key(ENTER)
                return
            if re.match(r"Move to Trash", selected, re.I):
                self.key(ESC)
                return
            check(not selected or selected != previous, "registry did not expose an editor/Trash return action")
            previous = selected
            self.key(DOWN)
        raise CheckFailure("could not identify editor/Trash registry context")

    def cancel_default(self):
        self.wait(lambda: "Cancel" in self.screen.text, "default-Cancel confirmation")
        check(bool(re.search(r">\s*Cancel", self.screen.text)), "confirmation did not select Cancel by default")
        self.key(ENTER)

    def confirm(self):
        self.wait(lambda: "Cancel" in self.screen.text, "confirmation")
        check(bool(re.search(r">\s*Cancel", self.screen.text)), "confirmation did not select Cancel by default")
        self.key(TAB)
        self.key(ENTER)

    def finish(self):
        self.key(ESC)
        self.wait(lambda: self.process.poll() is not None, "clean TUI exit", timeout=8)
        check(self.process.returncode == 0, "TUI did not exit cleanly")


def nested_sizes(home):
    note_id = home.seed("synthetic size fixture")
    before = home.snapshot()
    with Terminal(home) as terminal:
        for columns, rows in [(120, 40), (80, 24), (50, 12), (25, 5), (2, 2), (1, 1)]:
            terminal.action(r"^Find notes", "Find notes")
            terminal.wait(lambda: "Find" in terminal.screen.text, "search")
            terminal.key(b"\x1bOP")  # F1, preserved existing help shortcut.
            terminal.wait(lambda: "Help" in terminal.screen.text, "nested Help")
            terminal.resize(columns, rows)
            terminal.key(ESC)  # Help -> Search.
            check(terminal.process.poll() is None, "Escape from nested Help exited the app")
            terminal.key(ESC)  # Search -> editor.
            check(terminal.process.poll() is None, "Escape from Search exited the app")
            terminal.resize(120, 40)
        terminal.finish()
    check(home.note(note_id) is not None and home.snapshot() == before, "overlay cancellation/resize changed persisted notes")


def capability_parser(_home):
    replies = []
    screen = Screen(20, 5, replies.append)
    # Exercise byte-by-byte fragmentation, including both OSC terminators,
    # multibyte text and a cursor report after position changes.
    data = b"\x1b]11;?\x07\x1b]10;?\x1b\\\x1b[3;4H\x1b[6n" + "café".encode()
    for byte in data:
        screen.feed(bytes([byte]))
    check(replies == [b"\x1b]11;rgb:1e1e/1e1e/2e2e\x07",
                      b"\x1b]10;rgb:eeee/eeee/eeee\x07", b"\x1b[3;4R"],
          "fragmented background/foreground/cursor queries were not answered")
    check("café" in screen.text, "fragmented UTF-8 rendering failed")
    screen.feed(b"\x1b[2J\x1b[H> Synthetic action []")
    check(screen.text.splitlines()[0] == "> Synthetic action [", "VT cursor/erase parsing failed")


def conversion_cancel(home):
    home.seed("synthetic conversion original")
    before = home.snapshot()
    with Terminal(home, profile="legacy") as terminal:
        terminal.action(r"^(Toggle ghost mode|Convert.*ghost|Ghost mode)", "ghost conversion")
        terminal.cancel_default()
        terminal.finish()
    check(home.snapshot() == before, "default-Cancel ghost conversion modified original")


def locked_save(home):
    note_id = home.seed("synthetic lock")
    with Terminal(home) as terminal:
        terminal.key(b"\x05")  # Existing Ctrl+E smart end.
        with home.db() as db:
            db.execute("BEGIN IMMEDIATE")
            terminal.paste(" pending")
            terminal.wait(lambda: "Saving" in terminal.screen.text,
                          "pending save behind independent SQLite lock", timeout=4)
            terminal.resize(80, 24)
            terminal.paste(" latest")
            terminal.wait(lambda: "latest" in terminal.screen.text, "typing/resize while SQLite writer is locked", timeout=2)
            db.commit()
        terminal.pump(1.3)
        terminal.finish()
    check(home.note(note_id)["content"] == "synthetic lock pending latest", "pending-save reconciliation lost latest typed text")
    home.healthy()


def clipboard_isolation(home):
    # Exercise every shim directly, then verify that product Copy uses the shim
    # selected by its backend lookup. No payload bytes are recorded on disk.
    payload = b"synthetic clipboard probe"
    check(not home.clipboard_records(), "clipboard audit was not initially empty")
    check(all(name not in home.env for name in ("DISPLAY", "WAYLAND_DISPLAY", "WAYLAND_SOCKET", "DBUS_SESSION_BUS_ADDRESS")),
          "real desktop clipboard endpoints remain in child environment")
    for writer in ("pbcopy", "wl-copy", "xclip", "xsel"):
        result = subprocess.run([writer], input=payload, capture_output=True,
                                env=home.env, cwd=home.path, timeout=3)
        check(result.returncode == 0 and not result.stdout and not result.stderr,
              "clipboard writer shim failed")
    check(home.clipboard_matches(payload, expected_calls=4), "clipboard shim audit missed calls/bytes")
    home.clipboard_audit.unlink()
    home.seed(payload.decode())
    with Terminal(home) as terminal:
        terminal.action(r"^Copy note", "Copy note")
        terminal.wait(lambda: home.clipboard_matches(payload), "one exact synthetic clipboard write")
        terminal.finish()
    check(home.clipboard_matches(payload), "Copy issued extra calls or unexpected bytes")


def displayed_deadline(text):
    """Parse the confirmation's absolute local time, never the editor countdown."""
    for match in re.finditer(r"(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?)([^\n]{0,60})", text):
        date, clock, tail = match.groups()
        offset = re.search(r"([+-]\d{2}:?\d{2}|\bZ\b)", tail)
        zone = offset.group(1) if offset else ("+00:00" if "UTC" in tail else
                                              "-07:00" if "PDT" in tail else
                                              "-08:00" if "PST" in tail else None)
        if zone is not None:
            instant = parse_instant(date + "T" + clock + zone)
            # A minute-resolution display names a minute rather than its precise
            # second; stored deadline bounds below still require the full 900s.
            precision = 60 if clock.count(":") == 1 else 1
            return instant, precision
    raise CheckFailure("TTL confirmation lacks a parseable absolute local instant/offset")


def assert_preset(before, after, start, end, displayed, precision):
    check(after["id"] == before["id"] and after["content"] == before["content"],
          "TTL picker changed note identity/content")
    check(after["revision"] == before["revision"] + 1,
          "TTL picker did not advance exactly one CAS revision")
    check(after["expiration_source"] == "preset", "TTL picker did not record preset ownership")
    check(after["is_pinned"] == before["is_pinned"] and after["deleted_at"] == before["deleted_at"],
          "TTL picker changed unrelated lifecycle state")
    stored = parse_instant(after["expires_at"])
    check(start + dt.timedelta(seconds=900) <= stored <= end + dt.timedelta(seconds=900, milliseconds=1),
          "15-minute picker deadline is not 900 seconds after confirmation")
    check(-1 <= (stored - displayed).total_seconds() <= precision + 1,
          "stored deadline does not agree with displayed absolute local instant")


def preset_assertions(_home):
    start = dt.datetime(2026, 10, 12, 19, tzinfo=dt.timezone.utc)
    end = start + dt.timedelta(milliseconds=200)
    before = {"id": 7, "content": "synthetic preset", "revision": 4,
              "is_pinned": 0, "deleted_at": None}
    after = dict(before, revision=5, expiration_source="preset",
                 expires_at=utcstamp(start + dt.timedelta(seconds=900, milliseconds=100)))
    shown, precision = displayed_deadline("2026-10-12 12:15:00 PDT (-07:00)")
    assert_preset(before, after, start, end, shown, precision)
    for mutation in ({"expires_at": utcstamp(start + dt.timedelta(hours=1))},
                     {"revision": 4}, {"expiration_source": "directive"},
                     {"content": "wrong"}, {"id": 8}):
        try:
            assert_preset(before, dict(after, **mutation), start, end, shown, precision)
        except CheckFailure:
            continue
        raise CheckFailure("preset assertion accepted wrong deadline/ownership/revision/identity/content")


def expiration_picker(home):
    note_id = home.seed("synthetic picker")
    home.schema3()
    with Terminal(home) as terminal:
        before = home.snapshot()
        terminal.action(r"^Set expiration", "Set expiration")
        terminal.select(r"15 minutes", "15-minute preset")
        terminal.key(ENTER)
        terminal.wait(lambda: "Cancel" in terminal.screen.text, "absolute TTL confirmation")
        check(bool(re.search(r"\d{4}-\d{2}-\d{2}", terminal.screen.text)), "TTL confirmation lacks absolute local date")
        check(bool(re.search(r"(?:PDT|PST|UTC|[+-]\d{2}:?\d{2})", terminal.screen.text)), "TTL confirmation lacks zone/offset")
        terminal.four_sizes()
        terminal.cancel_default()
        check(home.snapshot() == before, "default-Cancel TTL choice changed persisted metadata")
        # Confirmation Cancel restores its parent picker, preserving selection.
        # Escape then closes that one layer before Commands is available again.
        terminal.wait(lambda: re.match(r"15 minutes", terminal.selected(), re.I) is not None,
                      "selected preset restored after default Cancel")
        terminal.key(ESC)
        terminal.wait(lambda: "synthetic picker" in terminal.screen.text,
                      "editor restored after picker Escape")
        terminal.action(r"^Set expiration", "Set expiration")
        terminal.select(r"15 minutes", "15-minute preset")
        terminal.key(ENTER)
        terminal.wait(lambda: "Cancel" in terminal.screen.text, "15-minute confirmation")
        displayed, precision = displayed_deadline(terminal.screen.text)
        prior = home.note(note_id)
        start = dt.datetime.now(dt.timezone.utc)
        terminal.confirm()
        terminal.wait(lambda: home.note(note_id)["expires_at"] is not None, "persisted preset deadline")
        end = dt.datetime.now(dt.timezone.utc)
        check((end - start).total_seconds() <= 6, "preset confirmation exceeded its bounded interval")
        preset = home.note(note_id)
        assert_preset(prior, preset, start, end, displayed, precision)
        terminal.action(r"^Set expiration", "Set expiration")
        terminal.select(r"Never", "Never preset")
        terminal.key(ENTER)
        terminal.confirm()
        terminal.wait(lambda: home.note(note_id)["expires_at"] is None, "Never clearing deadline")
        never = home.note(note_id)
        check(never["id"] == preset["id"] and never["content"] == preset["content"] and
              never["revision"] == preset["revision"] + 1 and never["expiration_source"] == "none" and
              never["is_pinned"] == preset["is_pinned"] and never["deleted_at"] == preset["deleted_at"],
              "Never did not clear ownership with exactly one revision and stable other fields")
        terminal.finish()
    check(home.note(note_id)["expiration_source"] == "none", "Never did not clear deadline ownership")


def pin_unpin(home):
    note_id = home.seed("synthetic pin\n#kill 1 hour")
    home.schema3()
    with Terminal(home, profile="legacy") as terminal:
        terminal.action(r"^(Pin or unpin|Pin note|Pin$)", "pin")
        terminal.wait(lambda: home.note(note_id)["is_pinned"] == 1, "persisted pin")
        check(home.note(note_id)["expires_at"] is None, "pin left a hidden expiration")
        pinned = home.snapshot()
        terminal.action(r"^(Pin or unpin|Unpin)", "unpin")
        terminal.four_sizes()
        terminal.cancel_default()
        check(home.snapshot() == pinned, "default-Cancel unpin changed note")
        terminal.action(r"^(Pin or unpin|Unpin)", "unpin")
        terminal.confirm()
        terminal.wait(lambda: home.note(note_id)["is_pinned"] == 0, "persisted unpin")
        terminal.finish()
    home.run("-q", "-l", "-n", "unrelated append after unpin")
    check(home.note(note_id)["expires_at"] is None, "unpin/restart/append resurrected directive deadline")


def assert_no_sentinel(home, sentinel):
    with home.db() as db:
        for table in ("notes", "notes_fts"):
            check(db.execute(f"SELECT COUNT(*) FROM {table} WHERE instr(content,?)>0", (sentinel,)).fetchone()[0] == 0, "ghost sentinel reached SQLite/index")
        check(db.execute("SELECT COUNT(*) FROM tags WHERE instr(tag,?)>0", (sentinel,)).fetchone()[0] == 0, "ghost sentinel reached tags")
    for path in home.path.rglob("*"):
        if path.is_file():
            # Includes WAL, SHM, backups, exports and process-created logs.
            with path.open("rb") as stream:
                tail = b""
                while chunk := stream.read(65536):
                    check(sentinel.encode() not in tail + chunk, "ghost sentinel reached a synthetic HOME file")
                    tail = chunk[-len(sentinel):]


def new_ghost(home):
    home.seed("synthetic persisted ghost neighbor")
    before = home.snapshot()
    sentinel = "M2_GHOST_MEMORY_ONLY_SENTINEL_8be638f1"
    ghost_text = sentinel + " #ghostprivate\n#kill 1 second"
    with Terminal(home) as terminal:
        terminal.action(r"^New ghost (draft|note)?", "New Ghost")
        terminal.paste(ghost_text)
        terminal.wait(lambda: "Ghost" in terminal.screen.text, "Ghost status")
        terminal.wait(lambda: sentinel in terminal.screen.text, "ghost sentinel accepted into editor")
        terminal.pump(3.2)  # Exceed the real >2-second autosave debounce plus tick.
        check(sentinel in terminal.screen.text, "ghost content disappeared while idle")
        check(not home.clipboard_records(), "ghost typing/idle invoked an external clipboard writer")
        check(home.snapshot() == before, "ghost idle/autosave changed persisted state")
        assert_no_sentinel(home, sentinel)
        # Explicit output and ghost-to-persisted requests must warn before any
        # external writer runs. Enter on their initial Cancel writes nothing.
        terminal.action(r"^Copy note", "ghost copy warning")
        check(not home.clipboard_records(), "ghost Copy wrote before confirmation")
        terminal.cancel_default()
        terminal.pump(0.3)
        check(not home.clipboard_records(), "ghost Copy cancellation invoked a clipboard writer")
        terminal.action(r"^Export note", "ghost export warning")
        terminal.cancel_default()
        terminal.action(r"^(Toggle ghost mode|Convert.*ghost|Ghost mode)", "ghost persistence warning")
        terminal.cancel_default()
        check(home.snapshot() == before, "canceled ghost output/persistence changed database state")
        check(not home.clipboard_records(), "canceled ghost output invoked a clipboard writer")
        # A deliberate Copy confirmation must issue exactly one writer call with
        # the intended bytes, confined to the disposable audit shim.
        terminal.action(r"^Copy note", "confirmed ghost copy warning")
        check(not home.clipboard_records(), "ghost Copy wrote before explicit confirmation")
        terminal.confirm()
        terminal.wait(lambda: home.clipboard_matches(ghost_text.encode()),
                      "one exact confirmed ghost clipboard write")
        terminal.action(r"^Find notes", "ghost search")
        terminal.paste(sentinel)
        terminal.wait(lambda: "No matches" in terminal.screen.text, "ghost absent from search")
        terminal.key(ESC)
        terminal.finish()
    home.run("-q")
    check(home.snapshot() == before, "ghost typing/search/exit changed persisted notes/tags/index")
    check(home.clipboard_matches(ghost_text.encode()), "ghost actions issued additional/unexpected clipboard writes")
    assert_no_sentinel(home, sentinel)


def safe_conversion(home):
    note_id = home.seed("synthetic safe conversion source")
    home.schema3()
    before = home.snapshot()
    with Terminal(home) as terminal:
        terminal.action(r"^(Toggle ghost mode|Convert.*ghost|Ghost mode)", "ghost conversion")
        terminal.four_sizes()
        terminal.select(r"(Duplicate.*ghost|Keep original|Copy.*ghost)", "keep-original ghost choice")
        terminal.key(ENTER)
        terminal.wait(lambda: "Ghost" in terminal.screen.text, "converted ghost status")
        terminal.key(b"\x05")
        terminal.paste(" memory-only addition")
        terminal.wait(lambda: "memory-only addition" in terminal.screen.text,
                      "converted ghost edit accepted into editor")
        terminal.pump(3.2)
        check("memory-only addition" in terminal.screen.text, "converted ghost edit disappeared while idle")
        terminal.finish()
    check(home.snapshot() == before and home.note(note_id) is not None, "safe ghost conversion changed/deleted original")


def inactive_expiry(home):
    home.seed("synthetic inactive neighbor")
    home.schema3()
    active_id = home.seed("synthetic edited neighbor")
    with Terminal(home) as terminal:
        terminal.wait(lambda: "synthetic edited neighbor" in terminal.screen.text,
                      "loaded neighbor before periodic-expiry fixture")
        # Capture via a separate CLI process only after the interactive startup
        # has rendered the active neighbor. The CLI performs startup maintenance
        # before this new directive is saved, so its later transition must occur
        # in the already-running TUI. This also uses the real parser/fingerprint.
        due_id = home.seed("synthetic short timer\n#kill 3 seconds")
        armed = home.note(due_id)
        check(armed["deleted_at"] is None and parse_instant(armed["expires_at"]) > dt.datetime.now(dt.timezone.utc),
              "periodic expiry fixture was not active with a future deadline")
        terminal.key(b"\x05")
        terminal.paste(" latest draft")
        terminal.wait(lambda: "latest draft" in terminal.screen.text,
                      "neighbor edit accepted before periodic expiry")
        check(home.note(due_id)["deleted_at"] is None, "inactive note expired before neighbor editing began")
        # QN-028 maintenance cadence is <=30 seconds; no system clock change.
        terminal.wait(lambda: home.note(due_id)["deleted_at"] is not None,
                      "periodic inactive short-directive expiry", timeout=38)
        expired = home.note(due_id)
        check(expired["delete_reason"] == "expired" and expired["revision"] == armed["revision"] + 1 and
              expired["content"] == armed["content"] and expired["expiration_source"] == "directive",
              "periodic expiry lost content/directive ownership or failed one-revision transition")
        terminal.finish()
    check(home.note(active_id)["content"] == "synthetic edited neighbor latest draft", "inactive expiry lost neighbor draft")


def external_expiry(home, lifecycle=True):
    note_id = home.seed("synthetic external expiry")
    if lifecycle:
        home.schema3()
    latest = "synthetic external expiry local recovery text"
    with Terminal(home) as terminal:
        terminal.key(b"\x05")
        # Own the writer transaction before typing: local autosave cannot commit
        # first, even if either process pauses beyond the debounce interval.
        with home.db() as db:
            db.execute("BEGIN IMMEDIATE")
            row = db.execute("SELECT revision FROM notes WHERE id=?", (note_id,)).fetchone()
            terminal.paste(" local recovery text")
            terminal.wait(lambda: "local recovery text" in terminal.screen.text,
                          "dirty local draft while external writer owns transaction", timeout=2)
            now = utcstamp(dt.datetime.now(dt.timezone.utc))
            reason = ",delete_reason='expired'" if lifecycle else ""
            result = db.execute("UPDATE notes SET deleted_at=?" + reason + ",revision=revision+1 WHERE id=? AND revision=? AND deleted_at IS NULL",
                                (now, note_id, row[0]))
            check(result.rowcount == 1, "external expiry fixture failed its CAS")
            check(db.execute("SELECT content FROM notes WHERE id=?", (note_id,)).fetchone()[0] == "synthetic external expiry",
                  "local autosave committed before controlled external expiry")
            db.commit()
        expired = home.note(note_id)
        terminal.key(ESC)  # Save/exit must report conflict and keep the draft.
        terminal.wait(lambda: "Conflict" in terminal.screen.text or "conflict" in terminal.screen.text,
                      "external-expiry draft recovery", timeout=8)
        check("local recovery text" in terminal.screen.text, "external expiry hid/discarded local text")
        terminal.key(b"c")  # Preserved registry conflict.copy key; not a future chord.
        def copied():
            with home.db() as db:
                return db.execute("SELECT COUNT(*) FROM notes WHERE id<>? AND content=? AND deleted_at IS NULL",
                                  (note_id, latest)).fetchone()[0] == 1
        terminal.wait(copied, "same-text separate-ID recovery copy")
        terminal.finish()
    check(home.note(note_id) == expired, "recovery copy overwrote the externally expired original")
    home.healthy()


def controlled_external_delete(home):
    # The existing schema-2 CAS recovery path validates writer ordering and the
    # PTY recovery harness before schema-3 lifecycle features are available.
    external_expiry(home, lifecycle=False)


def assert_trash_readonly(home, terminal, note_id):
    terminal.wait(lambda: "synthetic Trash source" in terminal.screen.text, "original read-only Trash preview")
    snapshot = home.snapshot()
    original = home.note(note_id)["content"]
    forbidden = "M2_MUST_NOT_EDIT_TRASH"
    terminal.paste(forbidden)
    check(forbidden not in terminal.screen.text and "synthetic Trash source" in terminal.screen.text,
          "Trash preview accepted local text editing or lost original preview")
    terminal.pump(3.2)
    check(forbidden not in terminal.screen.text and "synthetic Trash source" in terminal.screen.text,
          "Trash buffer accepted text during idle/autosave")
    check(home.snapshot() == snapshot, "Trash preview changed stored notes during idle/autosave")
    return snapshot, original, forbidden


def trash_readonly(home):
    note_id = home.seed("synthetic Trash source")
    with Terminal(home) as terminal:
        terminal.action(r"^Move to Trash", "Move to Trash")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is not None, "soft delete")
        terminal.action(r"^Open Trash", "Open Trash")
        assert_trash_readonly(home, terminal, note_id)
        terminal.ensure_editor()
        terminal.finish()
    check(home.note(note_id)["content"] == "synthetic Trash source", "read-only Trash lost original content")


def trash_roundtrip(home):
    note_id = home.seed("synthetic Trash source\n#kill 1 hour")
    home.schema3()
    with Terminal(home) as terminal:
        terminal.action(r"^Move to Trash", "Move to Trash")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is not None, "soft delete")
        terminal.action(r"^Open Trash", "Open Trash")
        trashed, original, forbidden = assert_trash_readonly(home, terminal, note_id)
        terminal.action(r"^Delete forever", "Delete forever")
        terminal.four_sizes()
        terminal.cancel_default()
        check(home.snapshot() == trashed, "default-Cancel permanent delete changed Trash")
        terminal.action(r"^Restore note", "Restore choices")
        terminal.four_sizes()
        terminal.cancel_default()
        check(home.snapshot() == trashed, "default-Cancel restore changed Trash")
        terminal.action(r"^Restore note", "Restore choices")
        terminal.select(r"Restore without timer", "restore without timer")
        terminal.key(ENTER)
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is None, "same-ID restore")
        restored = home.note(note_id)
        check(restored["expires_at"] is None, "safe restore rearmed directive")
        check(restored["content"] == original and forbidden not in terminal.screen.text,
              "read-only marker reached restored content/local buffer")
        terminal.ensure_editor()
        terminal.action(r"^Move to Trash", "second soft delete")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is not None, "second soft delete")
        terminal.action(r"Undo.*(Trash|trash)|Restore last", "Undo last move to Trash")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is None, "same-ID Trash undo")
        check(home.note(note_id)["content"] == original and forbidden not in terminal.screen.text,
              "read-only marker reached same-ID undo content/local buffer")
        terminal.action(r"^Move to Trash", "third soft delete")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is not None, "third soft delete")
        terminal.action(r"^Open Trash", "Open Trash")
        terminal.action(r"^Delete forever", "confirmed Delete forever")
        terminal.confirm()
        terminal.wait(lambda: home.note(note_id) is None, "confirmed permanent deletion")
        terminal.ensure_editor()
        terminal.finish()
    home.healthy()


BASELINE = [("fragmented terminal capability/UTF-8 parser", capability_parser),
            ("clipboard shims/endpoints/exact writer audit", clipboard_isolation),
            ("exact preset/ownership/revision assertion guards", preset_assertions),
            ("four sizes/tiny/nested Escape/keyboard", nested_sizes),
            ("legacy Commands/default-Cancel ghost", conversion_cancel),
            ("locked save/typing/resize/latest draft", locked_save),
            ("controlled external winner/local copy recovery", controlled_external_delete),
            ("Trash visible/stored read-only beyond debounce", trash_readonly)]
M2 = [("TTL picker/absolute confirmation/Never", expiration_picker),
      ("legacy pin/unpin/default Cancel/no rearm", pin_unpin),
      ("New Ghost/search/restart/DB/files audit", new_ghost),
      ("safe conversion preserves original", safe_conversion),
      ("inactive short directive expiry/neighbor edits", inactive_expiry),
      ("external expiry/local text/copy recovery", external_expiry),
      ("Trash read-only/restore/undo/forever/cancel", trash_roundtrip)]


if __name__ == "__main__":
    args = arguments(__doc__)
    raise SystemExit(run_suite(args.binary, BASELINE if args.baseline else BASELINE + M2))
