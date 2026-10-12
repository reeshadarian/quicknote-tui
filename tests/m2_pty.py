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

from m2_cli import Home, CheckFailure, MissingFeature, arguments, check, run_suite, utcstamp


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
        terminal.action(r"^Set expiration", "Set expiration")
        terminal.select(r"15 minutes", "15-minute preset")
        terminal.key(ENTER)
        terminal.confirm()
        terminal.wait(lambda: home.note(note_id)["expires_at"] is not None, "persisted preset deadline")
        terminal.action(r"^Set expiration", "Set expiration")
        terminal.select(r"Never", "Never preset")
        terminal.key(ENTER)
        terminal.confirm()
        terminal.wait(lambda: home.note(note_id)["expires_at"] is None, "Never clearing deadline")
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
    with Terminal(home) as terminal:
        terminal.action(r"^New ghost (draft|note)?", "New Ghost")
        terminal.paste(sentinel + " #ghostprivate\n#kill 1 second")
        terminal.wait(lambda: "Ghost" in terminal.screen.text, "Ghost status")
        terminal.pump(1.4)
        # Explicit output and ghost-to-persisted requests must warn before any
        # external writer runs. Enter on their initial Cancel writes nothing.
        terminal.action(r"^Copy note", "ghost copy warning")
        terminal.cancel_default()
        terminal.action(r"^Export note", "ghost export warning")
        terminal.cancel_default()
        terminal.action(r"^(Toggle ghost mode|Convert.*ghost|Ghost mode)", "ghost persistence warning")
        terminal.cancel_default()
        terminal.action(r"^Find notes", "ghost search")
        terminal.paste(sentinel)
        terminal.wait(lambda: "No matches" in terminal.screen.text, "ghost absent from search")
        terminal.key(ESC)
        terminal.finish()
    home.run("-q")
    check(home.snapshot() == before, "ghost typing/search/exit changed persisted notes/tags/index")
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
        terminal.pump(1.3)
        terminal.finish()
    check(home.snapshot() == before and home.note(note_id) is not None, "safe ghost conversion changed/deleted original")


def inactive_expiry(home):
    due_id = home.seed("synthetic short timer\n#kill 3 seconds")
    home.schema3()
    active_id = home.seed("synthetic edited neighbor")
    with Terminal(home) as terminal:
        terminal.key(b"\x05")
        terminal.paste(" latest draft")
        # QN-028 maintenance cadence is <=30 seconds; no system clock change.
        terminal.wait(lambda: home.note(due_id)["deleted_at"] is not None,
                      "periodic inactive short-directive expiry", timeout=38)
        check(home.note(due_id)["delete_reason"] == "expired", "inactive expiry lacks reason")
        terminal.finish()
    check(home.note(active_id)["content"] == "synthetic edited neighbor latest draft", "inactive expiry lost neighbor draft")


def external_expiry(home):
    note_id = home.seed("synthetic external expiry")
    home.schema3()
    latest = "synthetic external expiry local recovery text"
    with Terminal(home) as terminal:
        terminal.key(b"\x05")
        terminal.paste(" local recovery text")
        # Independent process-style SQLite write wins before the local debounce.
        # This fixture changes only synthetic metadata and explicitly advances CAS.
        with home.db() as db:
            row = db.execute("SELECT revision FROM notes WHERE id=?", (note_id,)).fetchone()
            now = utcstamp(dt.datetime.now(dt.timezone.utc))
            result = db.execute("UPDATE notes SET deleted_at=?,delete_reason='expired',revision=revision+1 WHERE id=? AND revision=? AND deleted_at IS NULL",
                                (now, note_id, row[0]))
            check(result.rowcount == 1, "external expiry fixture failed its CAS")
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


def trash_roundtrip(home):
    note_id = home.seed("synthetic Trash source\n#kill 1 hour")
    home.schema3()
    with Terminal(home) as terminal:
        terminal.action(r"^Move to Trash", "Move to Trash")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is not None, "soft delete")
        terminal.action(r"^Open Trash", "Open Trash")
        trashed = home.snapshot()
        terminal.paste("M2_MUST_NOT_EDIT_TRASH")
        terminal.pump(0.3)
        check(home.snapshot() == trashed, "Trash preview accepted text editing")
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
        check(home.note(note_id)["expires_at"] is None, "safe restore rearmed directive")
        # Return to editor whether restore navigates immediately or stays in Trash.
        if re.search(r"Trash", terminal.screen.text):
            terminal.key(ESC)
        terminal.action(r"^Move to Trash", "second soft delete")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is not None, "second soft delete")
        terminal.action(r"Undo.*(Trash|trash)|Restore last", "Undo last move to Trash")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is None, "same-ID Trash undo")
        terminal.action(r"^Move to Trash", "third soft delete")
        terminal.wait(lambda: home.note(note_id)["deleted_at"] is not None, "third soft delete")
        terminal.action(r"^Open Trash", "Open Trash")
        terminal.action(r"^Delete forever", "confirmed Delete forever")
        terminal.confirm()
        terminal.wait(lambda: home.note(note_id) is None, "confirmed permanent deletion")
        terminal.key(ESC)  # Trash -> editor.
        terminal.finish()
    home.healthy()


BASELINE = [("fragmented terminal capability/UTF-8 parser", capability_parser),
            ("four sizes/tiny/nested Escape/keyboard", nested_sizes),
            ("legacy Commands/default-Cancel ghost", conversion_cancel),
            ("locked save/typing/resize/latest draft", locked_save)]
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
