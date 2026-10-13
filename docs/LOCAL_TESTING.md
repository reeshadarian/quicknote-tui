# Local testing on Fedora

Use Go 1.25.9 or newer and GCC/CGO. Quicknote uses SQLite FTS5: pass `sqlite_fts5` to every test and build. Avoid `libsqlite3` unless intentionally using a verified system SQLite with FTS5.

Use GitButler for version-control operations. Read its installed skill before changing branches or committing. In the primary checkout, inspect `but status` and `but show dev/v2`; do not test on `main`. The configured GitButler target can still be `origin/main`: do not run `but land` or `but pull` blindly. Linked worktrees are unsupported by the installed CLI; do not run `but setup` in one. The living progress ledger records the verified dedicated-branch and coordinator integration workflow.

```zsh
go version
export CGO_ENABLED=1
export GOFLAGS='-tags=sqlite_fts5'
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/quicknote-v2-smoke .
```

A writable temporary `GOCACHE` can resolve sandbox cache permissions. Use a normal build in the primary checkout; if VCS stamping fails elsewhere and `-buildvcs=false` is needed, record that limitation rather than claiming a normal build passed.

Use an isolated HOME for manual checks. Do not overwrite HOME globally:

```zsh
QN_TEST_HOME="$(mktemp -d)"
HOME="$QN_TEST_HOME" /tmp/quicknote-v2-smoke -n "test note" -q
HOME="$QN_TEST_HOME" /tmp/quicknote-v2-smoke
```

Never test deletion, retention or migration against personal notes. Existing schema-1/schema-2 databases receive a unique, verified SQLite `VACUUM INTO` backup before upgrading to schema 3. A blind copy of a live WAL database is not a backup. Synthetic migration tests check recovery, IDs, note/tag/FTS counts, foreign keys, integrity, rollback and backup failure. No production backup or migration is implied by passing them.

Schema 3 records durable absolute TTL deadlines, their source/directive observation, pin state, deletion reason and CAS revision. Maintenance moves expired persisted notes into recoverable Trash. Trash remains recoverable at exactly 72 hours; purge occurs only after that boundary. Restore without a timer observes the existing directive so ordinary saves do not immediately rearm it. Pinning clears TTL; unpinning does not restore a hidden timer. Ghosts remain in the session unless persistence or external output is explicitly confirmed; discard does not promise secure erasure.

Repeated lifecycle, save ownership, CAS and snippet checks:

```zsh
CGO_ENABLED=1 go test -tags sqlite_fts5 -count=20 \
  -run 'TestQN02[89]|TestQN03[012]|TestQN011T17|TestQN012(T02|T24)|TestQN048' .
CGO_ENABLED=1 go test -tags sqlite_fts5 -run '^$' \
  -fuzz '^FuzzQN028KillDirective$' -fuzztime=1s -parallel=2 .
CGO_ENABLED=1 go test -tags sqlite_fts5 -run '^$' \
  -fuzz '^FuzzQN016SourceRows$' -fuzztime=1s -parallel=2 .
```

Model tests deliver typed asynchronous completions in deliberately different orders. Editing and resizing must remain responsive while SQLite saves are held. Pending operations must preserve the newest local draft and its actual CAS base. Exit waits for outstanding saves or exposes recoverable failure/conflict state.

Default Ctrl+K opens Commands; Alt+K deletes a line. Legacy Ctrl+K deletes a line and Alt+P opens Commands. Alt+T opens TTL, Alt+G creates a ghost, and Ctrl+G converts through default-Cancel choices. See `docs/KEYBOARD.md` for restore, Trash undo, delete and custom bindings. Test default and legacy profiles, nested overlays, layered Escape, 120×40/80×24/50×12/25×5 and tiny layouts. Verify paste, Unicode, preserved code/math snippets, default Cancel, restored timer behavior and independent duplicate-note identity.

Record exact commands, outputs, immutable commits and whether evidence is model, scripted PTY or human terminal testing. Scripted PTY checks do not verify human Fedora/Zsh/Kitty/tmux behavior, desktop clipboard integration, remote CI or release benchmarks.

Reproducible scripted acceptance (Linux/Unix PTYs, Python standard library):

```zsh
PYTHONDONTWRITEBYTECODE=1 python3 tests/m2_cli.py /tmp/quicknote-v2-smoke
PYTHONDONTWRITEBYTECODE=1 python3 tests/m2_pty.py /tmp/quicknote-v2-smoke
```

Run without `--baseline` for M2 acceptance. The scripts use disposable synthetic HOMEs and recording clipboard shims, bounded timeouts, real SQLite locks, and exact identity/revision/note/tag/FTS assertions. They test expiry, picker/Never, pin/unpin, ghost privacy and explicit output, safe original conversion, Trash read-only/Cancel/restore/reset/Undo/forever, no-timer save/restart, duplicate preservation, resizing and nested Escape. Expected full results are CLI10/10 and PTY15/15; any FAIL or MISSING fails acceptance. Tests do not print note payloads or invoke a real desktop clipboard.
