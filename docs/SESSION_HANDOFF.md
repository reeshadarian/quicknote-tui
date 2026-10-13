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
| QN-017 | a6141abfef965a0e3eacd1cc3e5bb1c03eb1f34b | Nested overlays, default-Cancel confirmations, safe reviewed replacements and recoverable errors |

The specification also lists **QN-048** in M1. It was completed in the subsequent QN-048 session below; **QN-028 is next READY**. Do not claim every M1/release feature is complete solely because QN-013–017 are done.

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

## Completed QN-048; next READY: QN-028 — Durable TTL engine

QN-048 is DONE in implementation commit `801f50262696b9be9e68b2a991b3b96000981b69`. `engine.go` and `compatibility_test.go` now cover T-35 math/code/snippets, Markdown/undo, headings/highlighting, units/dates and save identity. `:date:` aliases `:today:`. Malformed unary calls, negative/fractional/overflow factorials, NaN/Inf and equality text remain unchanged instead of crashing or inserting invalid values. All existing valid formats and shortcuts are preserved.

This session passed full tests/race/vet/build, twenty T-35 repetitions, bounded Unicode fuzz (21,229 executions), and built-binary scripted PTY calculation/date expansion/four-size/quiet CLI/SQLite integrity checks with Go 1.26.8-X:nodwarf5, CGO and sqlite_fts5. Human Fedora/tmux/clipboard, remote CI and release benchmarks were NOT RUN. Only synthetic temporary data was used; schema remains 2, main unchanged. See the living ledger for exact commands and corrected initial failures.

Next implement **QN-028** per the full specification: prerequisites QN-010/011/012 are complete. Durable TTL requires persisted absolute UTC deadlines, startup/periodic all-note expiry, an injectable clock, directive precedence and T-07/T-20. Follow the existing backup and data-preservation requirements before any schema/retention changes. Do not bundle TTL UI/pinning/Trash tasks.

Important files: `engine.go`, `compatibility_test.go`, `store.go`, `db.go`, `draft.go`, `tui_async.go`, `tui.go`, and existing migration/lifecycle tests. Inspect actual branch/ledger before choosing work.

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

> Continue https://github.com/reeshadarian/quicknote-tui on `dev/v2`. Read AGENTS.md, the full docs/QUICKNOTE_V2_SPEC.md, docs/IMPLEMENTATION_PROGRESS.md, docs/SESSION_HANDOFF.md and docs/LOCAL_TESTING.md; inspect current HEAD/status/log. QN-048 is committed and verified; next READY is **QN-028 durable TTL** unless newly directed or the branch has advanced. Preserve explicit draft identities, typed async/stale-result guards, CAS/conflict recovery, registry/keymap/layout/overlay contracts and all existing notes/CLI behavior. Use synthetic fixtures, verify SQLite backups before schema/retention changes, run CGO/sqlite_fts5 full tests/race/vet/build, commit only to dev/v2 and update exact SHA/test/limitation/handoff evidence. Never alter or merge main without permission or claim unexecuted checks passed.

## QN-028 isolated implementation awaiting coordinator review — 2026-10-12

The approved M2 execution now has QN-028 implementation in `qn-v2/QN-028-durable-ttl` from `cd909e4`. See the newest living-ledger entry for exact tests and limits; do not treat the earlier schema-2 descriptions as current after integrating this commit. Schema is 3, specification is v1.1.0. Independent review/integration/publication are coordinator-owned and still required before DONE.

New shared files: `lifecycle.go`, `migration_lifecycle.go`, `store_lifecycle.go`, `tui_lifecycle.go`. Constructors: `openDatabase(path, Clock)` returns startup/migration/maintenance errors; `NewSQLiteStoreWithClock` injects time. `LifecycleStore` extends `NoteStore`; metadata methods return snapshots from their successful transaction. `requestLifecycle` resolves dirty saves and serializes per-draft metadata operations; later UI tasks should call this seam rather than editing lifecycle fields or writing SQL. `ExpirationChoice` supports nil/Never and finite preset deadlines, with pinned/invalid/past values rejected. Restore without timer observes current directive; reset is permitted only for validated directive-owned duration. `DeleteTrashCAS` is trash-only; legacy generic `PurgeCAS` remains for transitional callers owned by QN-031/032.

Maintenance protects active/dirty/failed/saving drafts, serializes owned due-draft saves before expiry, reconciles newer text and stale results, and runs through overlays. Startup load returns maintenance changes even when the initial read becomes obsolete. Existing backups are never overwritten; orphan legacy tags stop migration for repair without dropping data. Only synthetic fixtures were used. Final full/race/vet/repeated CAS/fuzz/CLI gates passed; normal build VCS detection failed in `/tmp` and was verified with `-buildvcs=false`. Coordinator should run normal build from root and scripted PTY integration, then proceed to QN-029. Do not implement unrelated features or touch main.

QN-028 review follow-up: corrected full Markdown fence lengths/multiline code spans; expiry conflicts now dispatch comparison reads; stale opens preserve newest known draft/summary deletion state and enter Trash/recovery; stale successful lifecycle acknowledgments cannot undo an externally restored newer revision or its nested overlay parents. New regressions reproduce each review finding; original reviewer overlays and full/race/vet/build/fuzz/CLI gates pass. See ledger and coordinator task-1 report for exact correction commit/gates. Independent re-review is required before integration.

QN-028 correction round 2: a known newer summary now causes a full-note refetch, never revision promotion onto older text. Dirty drafts retain their original CAS base. Separate maintenance observations survive summary refresh; only a provably adjacent deletion can advance an acknowledged content snapshot without another read. New red/green regressions cover summary-induced overwrite, unseen-content revision gaps and refetch cancellation; final full/race/vet/build/repetition/CLI gates passed. See ledger and task-1 report for correction commit and exact evidence; await independent re-review before integrating.


## Approved M2 execution — QN-028 accepted

The user approved the M2 plan/design and all five tasks. QN-028 is DONE in `7c6d6c3`, `4f32720`, `a9d4c0c`; two independent reviews passed after corrections, full/race/vet/build/repetitions/fuzz/CLI evidence is in the living ledger. The primary checkout normal build passed. Schema3 and the approved specv1.1.0 amendment are implemented; no production DB was used. Next task QN-029, then030/031/032. This current section supersedes historical next-QN028 text. Preserve CAS content/revision pairing: superseded loads refetch complete notes; never promote stale text to a summary's revision. Agent execution ledger/reports are temporarily at `.superpowers/sdd/2026-10-12-m2-lifecycle/`; commits and living ledger are authoritative.

## QN-029 isolated implementation awaiting coordinator review

QN-029 is REVIEW in `qn-v2/QN-029-ttl-picker`, from accepted QN-028 base `7dac1a2`. See the newest living-ledger entry and coordinator task-2 report for exact commit/test evidence. It supplies TTL picker/resolver, Commands/default and legacy Alt+T/custom shortcut, local absolute default-Cancel confirmation, persisted countdown and a shared unclipped status click region. Custom accepts strict positive elapsed durations, explicit-offset RFC3339 or unique local minute input; DST gaps/folds require offsets. Relative choices resolve at final confirmation; pure View uses captured time. Dirty-save and metadata work share QN-028's queue with source guards and late-success reconciliation; do not replace content with a summary revision. Schema remains3, synthetic fixtures only. Coordinator owns independent review/integration/publication/DONE. Next task after acceptance QN-030, then031/032; those UI features remain disabled.
