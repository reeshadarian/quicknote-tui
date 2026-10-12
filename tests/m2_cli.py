#!/usr/bin/env python3
"""Reproducible synthetic M2 CLI acceptance. Requires an FTS5-enabled binary.

Usage: python3 tests/m2_cli.py /tmp/quicknote-m2 [--baseline]
Baseline runs only contracts that predate M2. Full runs never skip missing M2
features and return nonzero until they pass. No real HOME or note DB is opened.
"""

import argparse
import contextlib
import datetime as dt
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile


class CheckFailure(Exception):
    pass


class MissingFeature(CheckFailure):
    pass


def check(condition, message):
    if not condition:
        raise CheckFailure(message)


def utcstamp(instant):
    return instant.astimezone(dt.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def parse_instant(value):
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(dt.timezone.utc)


class Home:
    """Each scenario gets a fresh HOME, CWD, XDG roots and synthetic database."""

    def __init__(self, binary):
        self.binary = str(Path(binary).resolve())
        self.temp = tempfile.TemporaryDirectory(prefix="quicknote-m2-")
        self.path = Path(self.temp.name)
        self.db_path = self.path / ".local/share/quicknote-tui/notes.sqlite"
        self.env = dict(os.environ, HOME=str(self.path), TERM="xterm-256color",
                        XDG_CONFIG_HOME=str(self.path / ".config"),
                        XDG_DATA_HOME=str(self.path / ".local/share"),
                        XDG_CACHE_HOME=str(self.path / ".cache"),
                        XDG_STATE_HOME=str(self.path / ".local/state"),
                        TMPDIR=str(self.path), TZ="America/Los_Angeles")

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.temp.cleanup()

    def run(self, *args, data=b"", success=True):
        try:
            result = subprocess.run([self.binary, *args], input=data,
                                    capture_output=True, env=self.env,
                                    cwd=self.path, timeout=20)
        except subprocess.TimeoutExpired:
            raise CheckFailure("CLI exceeded 20-second process timeout") from None
        check(result.stdout == b"", "quiet CLI wrote to stdout")
        if success:
            check(result.returncode == 0, "quiet CLI failed (diagnostic content suppressed)")
            check(result.stderr == b"", "quiet CLI emitted an unexpected diagnostic")
        else:
            check(result.returncode != 0, "injected failure returned success")
            check(bool(result.stderr.strip()), "injected failure had no stderr diagnostic")
        return result

    @contextlib.contextmanager
    def db(self):
        # A synthetic DB must already have been created by this binary.
        check(self.db_path.is_file(), "binary did not create its synthetic database")
        connection = sqlite3.connect(self.db_path, timeout=2)
        connection.row_factory = sqlite3.Row
        try:
            yield connection
            connection.commit()
        finally:
            connection.close()

    def schema3(self):
        with self.db() as db:
            version = db.execute("PRAGMA user_version").fetchone()[0]
            columns = {r[1] for r in db.execute("PRAGMA table_info(notes)")}
        required = {"expires_at", "is_pinned", "revision", "expiration_source",
                    "expiration_needs_review", "delete_reason"}
        if version != 3 or not required.issubset(columns):
            raise MissingFeature("QN-028 schema 3 lifecycle metadata is unavailable")

    def seed(self, text):
        self.run("-q", "-n", text)
        with self.db() as db:
            return db.execute("SELECT id FROM notes ORDER BY id DESC LIMIT 1").fetchone()[0]

    def note(self, note_id):
        with self.db() as db:
            row = db.execute("SELECT * FROM notes WHERE id=?", (note_id,)).fetchone()
            return dict(row) if row else None

    def snapshot(self):
        with self.db() as db:
            return (db.execute("PRAGMA user_version").fetchone()[0],
                    tuple(tuple(r) for r in db.execute("SELECT * FROM notes ORDER BY id")),
                    tuple(tuple(r) for r in db.execute("SELECT * FROM tags ORDER BY note_id,tag")),
                    tuple(tuple(r) for r in db.execute("SELECT rowid,content FROM notes_fts ORDER BY rowid")))

    def healthy(self):
        with self.db() as db:
            check(db.execute("PRAGMA integrity_check").fetchone()[0] == "ok", "SQLite integrity_check failed")
            check(not db.execute("PRAGMA foreign_key_check").fetchall(), "SQLite foreign_key_check failed")
            check(db.execute("SELECT COUNT(*) FROM notes").fetchone()[0] ==
                  db.execute("SELECT COUNT(*) FROM notes_fts").fetchone()[0], "notes/FTS row counts differ")


def quiet_capture(home):
    home.run("-q", data=b"synthetic capture #m2cli")
    with home.db() as db:
        rows = db.execute("SELECT id,content,revision FROM notes").fetchall()
        check(len(rows) == 1 and rows[0][1] == "synthetic capture #m2cli", "quiet stdin capture changed content/count")
        note_id, _, revision = rows[0]
        check(db.execute("SELECT tag FROM tags WHERE note_id=?", (note_id,)).fetchone()[0] == "#m2cli", "capture tag was not indexed")
    home.run("-q", "-l", "-n", "synthetic append")
    after = home.note(note_id)
    check(after["content"] == "synthetic capture #m2cli\nsynthetic append", "last-note append changed content/identity")
    check(after["revision"] == revision + 1, "append did not advance exactly one CAS revision")
    before = home.snapshot()
    home.run("-q")
    check(home.snapshot() == before, "quiet no-input startup changed ordinary content/revisions")
    home.healthy()


def title_append(home):
    home.run("-q", "-t", "Synthetic M2 title", "-n", "first")
    with home.db() as db:
        note_id, revision = db.execute("SELECT id,revision FROM notes").fetchone()
    home.run("-q", "-t", "synthetic m2 title", "-n", "second")
    row = home.note(note_id)
    check(row["content"] == "# Synthetic M2 title\n\nfirst\nsecond", "title append did not preserve original identity/content")
    check(row["revision"] == revision + 1, "title append revision was not CAS advanced")
    with home.db() as db:
        check(db.execute("SELECT COUNT(*) FROM notes").fetchone()[0] == 1, "title append inserted a second note")


def write_failure(home):
    home.seed("synthetic unchanged #before")
    with home.db() as db:
        db.execute("CREATE TRIGGER m2_fail_update BEFORE UPDATE OF content ON notes BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END")
    before = home.snapshot()
    home.run("-q", "-l", "-n", "rejected append #after", success=False)
    check(home.snapshot() == before, "write error changed content/revision/tags/FTS")
    with home.db() as db:
        db.execute("DROP TRIGGER m2_fail_update")
    home.run("-q", "-l", "-n", "retry succeeds")
    home.healthy()


def tag_failure(home):
    home.seed("synthetic original #before")
    with home.db() as db:
        db.execute("CREATE TRIGGER m2_fail_tag BEFORE INSERT ON tags BEGIN SELECT RAISE(ABORT,'synthetic tag failure'); END")
    before = home.snapshot()
    home.run("-q", "-l", "-n", "rejected #after", success=False)
    check(home.snapshot() == before, "tag error left a partial content/revision/index write")
    home.healthy()


def migration_refusal(home):
    home.seed("synthetic newer schema")
    with home.db() as db:
        db.execute("PRAGMA user_version=999")
    before = home.snapshot()
    result = home.run("-q", "-n", "must not insert", success=False)
    check(b"version" in result.stderr.lower() or b"schema" in result.stderr.lower(), "newer-schema error lacks an actionable schema diagnostic")
    check(home.snapshot() == before, "unsupported-schema refusal changed notes/revisions/tags/FTS/version")


def directive_deadline(home):
    start = dt.datetime.now(dt.timezone.utc)
    note_id = home.seed("synthetic timer\n#kill 1 hour")
    home.schema3()
    first = home.note(note_id)
    check(first["expires_at"] is not None, "directive save did not persist an absolute deadline")
    delta = (parse_instant(first["expires_at"]) - start).total_seconds()
    check(3599 <= delta <= 3620, "directive deadline is outside the expected elapsed-time interval")
    check(first["expiration_source"] == "directive", "directive deadline ownership missing")
    home.run("-q", "-l", "-n", "unrelated append")
    after = home.note(note_id)
    check(after["expires_at"] == first["expires_at"], "unrelated CLI append reset the deadline")
    check(after["revision"] == first["revision"] + 1, "directive append revision mismatch")
    home.run("-q")
    check(home.note(note_id)["expires_at"] == first["expires_at"], "restart reset persisted deadline")


def startup_expiry(home):
    due_id = home.seed("synthetic due #duetag\n#kill 1 hour")
    home.schema3()
    active_id = home.seed("synthetic active")
    due = utcstamp(dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1))
    with home.db() as db:
        db.execute("UPDATE notes SET expires_at=? WHERE id=?", (due, due_id))
    before = home.note(due_id)
    active = home.note(active_id)
    home.run("-q")
    expired = home.note(due_id)
    check(expired is not None and expired["deleted_at"] is not None, "startup did not move inactive due note into Trash")
    check(expired["delete_reason"] == "expired", "startup expiry reason was not stored")
    check(expired["revision"] == before["revision"] + 1, "expiry revision did not advance once")
    check(expired["content"] == before["content"], "expiry lost content")
    check(home.note(active_id) == active, "expiry changed unrelated active note")
    once = home.snapshot()
    home.run("-q")
    check(home.snapshot() == once, "repeated startup expiry changed deletion time/revision")
    home.run("-q", "-l", "-n", "active append")
    check(home.note(due_id) == expired, "last-note CLI append targeted a Trash entry")
    home.healthy()


def maintenance_failure(home):
    note_id = home.seed("synthetic maintenance\n#kill 1 hour")
    home.schema3()
    with home.db() as db:
        db.execute("UPDATE notes SET expires_at=? WHERE id=?", (utcstamp(dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=1)), note_id))
        db.execute("CREATE TRIGGER m2_fail_expiry BEFORE UPDATE OF deleted_at ON notes BEGIN SELECT RAISE(ABORT,'synthetic maintenance failure'); END")
    before = home.snapshot()
    home.run("-q", "-n", "must not capture after maintenance failure", success=False)
    check(home.snapshot() == before, "maintenance error changed content/revisions or allowed capture")
    with home.db() as db:
        db.execute("DROP TRIGGER m2_fail_expiry")
    home.run("-q")
    check(home.note(note_id)["deleted_at"] is not None, "maintenance could not retry after failure")


BASELINE = [("quiet stdin/last append/revisions", quiet_capture),
            ("title append/identity", title_append),
            ("write failure/atomic retry", write_failure),
            ("tag failure/atomic rollback", tag_failure),
            ("unsupported migration refusal", migration_refusal)]
M2 = [("directive deadline/restart/append", directive_deadline),
      ("quiet startup inactive expiry/idempotence", startup_expiry),
      ("maintenance failure/refusal/retry", maintenance_failure)]


def run_suite(binary, scenarios):
    failed = 0
    for name, scenario in scenarios:
        try:
            with Home(binary) as home:
                scenario(home)
            print("PASS " + name, flush=True)
        except MissingFeature as error:
            failed += 1
            print("MISSING " + name + ": " + str(error), flush=True)
        except CheckFailure as error:
            failed += 1
            print("FAIL " + name + ": " + str(error), flush=True)
        except Exception as error:
            failed += 1
            # DB/subprocess exceptions can contain SQL or payloads; print type only.
            print("HARNESS " + name + ": " + type(error).__name__ + " (details suppressed)", flush=True)
    print(f"RESULT {len(scenarios) - failed}/{len(scenarios)} passed; {failed} failed/missing")
    return 1 if failed else 0


def arguments(description):
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument("binary", help="path to a built FTS5-enabled quicknote binary")
    parser.add_argument("--baseline", action="store_true", help="run only pre-M2 harness checks; does not verify M2")
    args = parser.parse_args()
    parser.error("binary must be an executable file") if not (Path(args.binary).is_file() and os.access(args.binary, os.X_OK)) else None
    return args


if __name__ == "__main__":
    args = arguments(__doc__)
    raise SystemExit(run_suite(args.binary, BASELINE if args.baseline else BASELINE + M2))
