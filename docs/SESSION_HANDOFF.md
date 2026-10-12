# Quicknote v2 — Session handoff

Repository: https://github.com/reeshadarian/quicknote-tui
Working branch: `dev/v2`. Protected branch: `main` — no modification/merge without permission.
Updated: 2026-10-12 UTC.

## Completed requested range

The maintainer requested **QN-013 through QN-017**. All five are DONE in independently verified implementation commits:

| Task | Commit | Foundation delivered |
|---|---|---|
| QN-013 | `7cb44decf4155a19edf40ea77c4e7adb620b93c3` | Explicit EditorDraft, stable persisted/memory identity, exact text, isolated bounded history and source maps |
| QN-014 | `f2320f2f916a985add919c9549576aff22717206` | Executable action registry, context/enabled reasons, generated help |
| QN-015 | `e64bebb6c09292c58b0dae6417bfdb22f7b431b6` | Versioned default/legacy/custom keyboard profiles, validation and basic Commands menu |
| QN-016 | `ffd2bb210d9d3bc412f56f17a18c59a0efdf936e` | Shared grapheme/cell wrapping, cursor/mouse mapping and responsive pure panels |
| QN-017 | QN017_SHA_PENDING | Nested overlays, default-Cancel confirmations, safe reviewed replacements and recoverable errors |

The specification also lists **QN-048** in M1. It remains outside that explicitly requested range and is **next READY**, followed by applicable M2/M3 tasks. Do not claim every M1/release feature is complete solely because QN-013–017 are done.

## Authoritative context

Read `AGENTS.md`, the **entire** `docs/QUICKNOTE_V2_SPEC.md`, `docs/IMPLEMENTATION_PROGRESS.md`, this file and `docs/LOCAL_TESTING.md` before changing code. The living progress ledger contains exact task commits, source changes, verification/failures and remaining limitations. `docs/IMPLEMENTATION_PROGRESS_TEMPLATE.md` is historical; never replace the living ledger with it. `docs/KEYBOARD.md` documents active profiles/configuration.

No prior chat or uploaded files are needed. Inspect actual git HEAD/status/log before relying on historical descriptions.

## Current contracts

- `EditorDraft.Note` is the authoritative identity/base revision. `index` only navigates summaries; reordering cannot redirect a save. ID-zero drafts have separate in-memory SessionIDs/keys, with no schema persistence. Per-draft buffer/history/cursor/viewport/error/local-version state is deeply snapshotted.
- Source maps connect byte/rune/grapheme/cell positions. Tabs and intentional whitespace/newlines are preserved; ordinary keys no longer append a synthetic newline. Vertical motion and mouse coordinates use the same physical-row layout as rendering. One-cell over-wide display fallback never alters source text.
- QN-012 typed workers capture immutable values and store handles. Saves serialize per draft and reconcile only matching requests. Newer edits remain dirty and chain CAS; inactive errors stay attached to their draft. Loads/highlights/searches reject obsolete note/draft/version/generation results. Exit waits for outstanding saves and exposes failures.
- QN-011 CAS remains enforced for content/tags/metadata. Production SQLite uses `_busy_timeout=5000&_txlock=immediate`. Schema remains `user_version 2`; legacy backup/migration/recovery fixtures pass. Never adopt a fresh revision and overwrite automatically.
- Registry-generated keyboard/help/menu definitions include context and explicit disabled reasons for future tasks. The basic Commands menu is usable; the full fuzzy palette remains QN-023.
- Default keys: **Ctrl+K Commands**, **Alt+K delete line**. Legacy: **Ctrl+K delete line**, **Alt+P Commands**. `~/.config/quicknote-tui/keyboard.json` version 1 supports validated custom action-ID bindings. Core modal cancellation/confirmation keys remain reachable. First launch shows a nonblocking migration notice.
- Nested search/help/Commands/export/filter/error overlays preserve parent focus and input. Escape closes one layer and invalidates obsolete reads; modal mouse events cannot edit through to the draft. Shared panels preserve cancel controls at 25×5 and safely handle 1×1/2×2.
- Destructive ghost conversion/legacy empty-note purge, overwrite and shell replacement require explicit confirmation, default Cancel, and a matching source draft/version. Persisted-to-ghost conversion warns about prior disk copies and does not claim secure erasure. Safe ghost creation/conversion redesign remains QN-031.
- Export checks/writes are asynchronous and atomic; unconfirmed new paths cannot overwrite a racing file. Backup/restore and full export work remain QN-041. Shell results are reviewed/undoable, but execution itself is still synchronous; async timeout/output bounds/cancel remain QN-039.
- Conflict recovery remains **C** copy, **I** compare, arrows/PageUp/PageDown scroll, **R twice** confirmed reload and **Esc** keep editing (or configured conflict bindings). Failed inactive saves block exit; a successful copy acknowledges only that recovered version and later edits revoke acknowledgment.

## Verification and limits

Each task passed full Go tests, race, vet, build and formatting/diff checks with Go 1.25.9/CGO/`sqlite_fts5`. There are 31 new regression tests across this range plus the Unicode source-row fuzz target. Existing T-01–04/T-17/T-02/T-24, migration/recovery and CLI suites remain passing.

Additional checks: 20 repeated concurrency/async/keymap/layout/overlay acceptance runs; bounded Unicode-layout fuzz (27,887 executions); built-binary scripted PTY tests for locked saves with continued typing/resize, conflict compare/copy and safe exit, four sizes, nested focus/layered Escape, default-Cancel/confirmed export and ghost default-Cancel, exact Unicode/tab text. Quiet CLI positive/negative capture, CAS revision progression, failure rollback and SQLite integrity passed using temporary fixtures.

Not verified: human-operated Fedora/Zsh/Kitty/tmux/clipboard matrix, remote GitHub CI or release benchmarks. Automated PTY is not manual Fedora evidence. No real notes, production backup/migration, main changes, dependency upgrade or new schema version.

## Next task: QN-048 — Preserve math/code/snippets

Add focused compatibility coverage for existing math/code modes, calculations, formatting, snippets, heading extraction and date/units (T-35), fixing only demonstrated regressions. Preserve the new draft/action/keymap/layout/overlay contracts and CAS/async checks. Required dependencies QN-004 and QN-013 are satisfied. Afterwards select the earliest ready P1 task from the living ledger; do not silently bundle M2/M3 features.

Important files: `draft.go`, `actions.go`, `keymap.go`, `layout.go`, `overlays.go`, `tui.go`, `tui_async.go`, `tui_conflict.go`, `engine.go`, `store.go`, the matching new `*_test.go` files and existing `cas_*test.go`.

## Fedora / Zsh gates

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
```

Synthetic manual fixtures only:

```zsh
export QN_TEST_HOME="$(mktemp -d)"
qntest() { HOME="$QN_TEST_HOME" /tmp/quicknote-test "$@"; }
printf 'one\ntwo\n' | qntest -q
qntest
```

Never run destructive tests against real `~/.local/share/quicknote-tui/notes.sqlite`. Use SQLite backup/`VACUUM INTO` for existing DB backups, not a blind copy of a live WAL file.

## Ready-to-paste continuation

> Continue https://github.com/reeshadarian/quicknote-tui on `dev/v2`. Read AGENTS.md, the full docs/QUICKNOTE_V2_SPEC.md, docs/IMPLEMENTATION_PROGRESS.md, docs/SESSION_HANDOFF.md and docs/LOCAL_TESTING.md; inspect current HEAD/status/log. QN-013–017 are committed and verified; implement **QN-048 math/code/snippet compatibility** with T-35 coverage unless the branch has advanced. Preserve explicit draft identities, typed async/stale-result guards, CAS/conflict recovery, registry/keymap/layout/overlay contracts and all existing notes/CLI behavior. Run CGO/sqlite_fts5 full tests/race/vet/build, commit only to dev/v2 and update exact SHA/test/limitation/handoff evidence. Never alter or merge main without permission, ask for documents already in the repository, or claim unexecuted checks passed.
