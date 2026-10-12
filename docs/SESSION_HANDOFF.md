# Quicknote v2 — New Session Handoff

Repository: https://github.com/reeshadarian/quicknote-tui
Working branch: `dev/v2`
Protected branch: `main` — do not modify or merge without explicit approval.
Latest task: **QN-012 implemented and verified**, 2026-10-12 UTC.
Implementation commit: `15dfba9cc8145eee51782d5f3c1b7463f81ec74b`.
Next task: **QN-013 — EditorDraft and authoritative NoteID**.

## Authoritative documents

Read these before changing code:

1. `AGENTS.md` — working protocol and branch/data safety rules.
2. `docs/QUICKNOTE_V2_SPEC.md` — full 987-line v1.0.0 specification; read the whole document, not only the task table.
3. `docs/IMPLEMENTATION_PROGRESS.md` — living task/commit/test ledger, including historical limitations and the latest QN-012 evidence.
4. `docs/LOCAL_TESTING.md` — Go/CGO/SQLite FTS5 commands and isolated test HOME.
5. `docs/QN-001-BASELINE.md` — original regression baseline.

`docs/IMPLEMENTATION_PROGRESS_TEMPLATE.md` is the archived original planning ledger. Do not replace the living progress file with it. All project context is in this repository; no upload or prior conversation is needed.

## Current development state

QN-001–QN-010 are implemented, with maintainer-reported Go/race and manual Fedora checks. QN-011 implementation is `595dd7da34d47852b511349e4750f11862c3d161`; extra concurrency coverage is `7b41a3e1ce960be91455688df97ef234c577e2ec`. Their fixture tests also passed during QN-012.

QN-012 now runs TUI save/load/search/highlight, metadata actions, conflict reads/copy/reload and help reads through typed Tea commands. Commands capture immutable snapshots/store values, not mutable model buffers or maps. SQL no longer runs on normal typing/navigation event paths. Shell filters, export and clipboard retain legacy behavior until their assigned tasks.

- Saves have note ID, base revision, local version and request ID; one write per note is in flight. Completions update the matching session's base revision, retain newer edits as dirty, and chain another CAS save. Duplicate/stale responses cannot acknowledge a newer request.
- Loads keep the source draft until a successful matching response. Source ID/version and open generation reject late/canceled responses. An acknowledged session revision cannot be regressed by an older load.
- Search debounces 100 ms, invalidates old selectable results and checks generation/query/trash scope. Search cancel preserves the original draft without initiating a save. Selecting a result still resolves dirty-save/conflict before opening its stable ID.
- Highlights check note ID, local version, generation and text; equal text across notes or after undo cannot bypass identity/version guards.
- Failed inactive saves remain attached to their own retained sessions. Exit waits for every outstanding save and reopens failed drafts for recovery. Copy recovery acknowledges only the copied original version; later edits invalidate that acknowledgment.
- Restore targets the restored ID and preserves the prior in-memory legacy timer reset. Durable TTL and explicit expired-note restore choices remain future tasks.

CAS SQL and schema remain unchanged: `user_version 2`, production `_busy_timeout=5000&_txlock=immediate`, revision guards for content/tags/metadata, and legacy v1 backup/migration/recovery fixtures. Never refresh a revision and automatically overwrite a conflicting draft.

Conflict keys: **C** save copy, **I** compare, arrows/PageUp/PageDown scroll, **R twice** confirm reload/discard, **Esc** keep editing. Copy/reload are asynchronous; Escape cancels a pending reload. Enter from a persistent save-error status retries/reopens recovery. Remote trash/purge still allows copy recovery.

## Verification and limits

QN-012 full suite, race detector, vet, build and format checks passed with Go 1.25.9/CGO/`sqlite_fts5`. T-02/T-24 and separate-connection/process T-17 passed 20 repeated runs. Tests cover a held SQLite writer lock, concurrent workers alongside edits/resize, 100 deterministic edit/save cycles, stale/duplicate completions, draft/history restoration, failed reads/writes, copy/reload cancellation and safe exit.

Built-binary scripted PTY checks passed for typing while a writer lock delayed autosave, preservation of newest edits, external conflict, compare/copy, four sizes (120×40, 80×24, 50×12, 25×5), and exit after recovery. Isolated CLI success/failed-write output/exit/rollback and SQLite integrity checks passed. Existing migration/backup recovery tests ran in the full/race suites.

These are container/automated results. Human-operated Fedora/Zsh/Kitty/tmux, the full platform/clipboard matrix, release benchmarks and remote CI were not verified in the QN-012 session. Do not turn historical pass records into new-session claims.

## QN-013 scope

Read spec §§3.2–3.4 and §5, and the exact task/dependency/acceptance table in §14.

Replace index-driven editor identity with an explicit EditorDraft/NoteID contract. Preserve per-draft undo/redo, dirty/error state, cursor, viewport, source maps, local versions and base revisions. Keep the QN-012 command protocol and CAS conflict recovery intact. Do not turn this into a new package/schema/keymap/UI rewrite.

Inspect `tui_async.go`, `tui.go` (`noteSession`, stash/install), `tui_conflict.go`, `store.go`, `engine.go`, `tui_async_test.go`, `async_test_helpers_test.go` and `cas_*test.go`. Add focused regressions before refactoring. **T-01–04 and T-17 must pass**, alongside QN-012 T-02/T-24 and the full suite/race/vet/build gates.

Remaining known follow-ups: ID-zero draft/ghost bookkeeping, editor trailing-newline/tab/grapheme layout consistency, legacy Store adapters, permanent-delete confirmation from Trash (QN-032), durable TTL (QN-028), safe ghost conversion (QN-031), parsed/fuzzy search (QN-018–021), asynchronous shell review (QN-039), and byte-preserving CLI (QN-040). Address only what the selected task actually requires.

## Fedora / Zsh commands

```zsh
git fetch origin
git switch dev/v2
git pull --ff-only origin dev/v2
git status --short
git log -5 --oneline
go version
export CGO_ENABLED=1
export GOFLAGS='-tags=sqlite_fts5'
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/quicknote-test .
go test -count=20 -run 'TestQN012(T02|T24)|TestQN011T17' .
```

For manual checks, use synthetic data only:

```zsh
export QN_TEST_HOME="$(mktemp -d)"
qntest() { HOME="$QN_TEST_HOME" /tmp/quicknote-test "$@"; }
printf 'one\ntwo\n' | qntest -q
qntest
```

Never test destructive operations/migrations against real `~/.local/share/quicknote-tui/notes.sqlite`. Existing database backups must use SQLite backup/`VACUUM INTO`, not a blind copy of a live WAL database.

## Ready-to-paste next-session prompt

> Continue Quicknote v2 at https://github.com/reeshadarian/quicknote-tui on `dev/v2`. Read `AGENTS.md`, the entire `docs/QUICKNOTE_V2_SPEC.md`, `docs/IMPLEMENTATION_PROGRESS.md`, `docs/SESSION_HANDOFF.md` and `docs/LOCAL_TESTING.md`; inspect actual branch HEAD and recent commits. Implement **QN-013 EditorDraft and authoritative NoteID** as specified, preserving the verified QN-011 CAS and QN-012 typed async/stale-result protocols. Run T-01–04, T-17, T-02/T-24 and the full Go/race/vet/build suite using CGO and `sqlite_fts5`. Preserve unsaved drafts, per-note history and CLI compatibility. Commit directly to `dev/v2`, update the living progress ledger and handoff with exact SHAs, tests, limitations and next READY task. Never modify or merge into `main` without permission; do not ask for documents already in the repository or claim unexecuted tests passed.
