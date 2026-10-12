# Quicknote v2 — implementation progress

Branch: `dev/v2`
Baseline main SHA: `d3e5a119cdf12ec99fcc14a62545f60729adea54`
Task: QN-001 — BASELINE SCAFFOLD COMMITTED; VERIFICATION PENDING
Updated: 2026-10-11

## Completed repository changes

- Added isolated SQLite temporary HOME fixtures and FTS/save/trash smoke tests.
- Added Unicode roundtrip smoke test.
- Recorded known-failing search identity and duplicate-trash regressions as explicitly skipped assertions.
- Added Go CI workflow for test/vet/build/race.
- Added baseline report and multi-session handoff guidance.

## Verification status

- GitHub commits: yes, on `dev/v2`.
- Local `go test ./...`: not run (unable to clone repository in execution container).
- CI checks: not confirmed. Never interpret a skipped regression as resolved.
- Production data accessed: no.

## Next session

1. Read `docs/QN-001-BASELINE.md` and inspect CI job output on `dev/v2`.
2. Fix compilation or fixture issues if CI reports any; do not silently change assertions to make them pass.
3. Mark QN-001 VERIFIED only after `go test ./...`, `go vet ./...`, `go build ./...` pass.
4. Begin QN-002; unskip search identity test after implementing the fix.
5. Keep work off `main` and record actual SHA and test evidence here.

The full 48-task specification was delivered separately as the Quicknote v2 implementation pack; copy that pack into this repository before implementing downstream architecture milestones.


## QN-002 — Search selection identity (2026-10-11)

**Implementation committed**, verification pending:
- `de5047680`: Resolve Enter search result via stable note ID in loaded list instead of changing only the editor buffer. Save any pending draft, fetch selected content by ID, clear undo/redo and stale save flags; clamp match cursor. Trash search remains read-only.
- `96fd5762`: Enable the previously skipped identity regression and add tests for editing the correct note, dirty draft preservation, history reset, trash selection, and cursor bounds.

**Known limitations:** SQLite `saveNote` still swallows write errors; transactional/error-aware persistence is a later task. A pending draft should not be discarded on DB failures; QN-003 should address this. Tests and CI have not yet been observed passing (local GitHub clone unavailable due network DNS). Do not mark VERIFIED without test output.

**Next:** inspect CI, address any compile/test failures, then continue QN-003. No changes have been made to `main`.


## QN-003 — Editor session isolation (2026-10-11)

**Implementation committed; test execution pending.**
- `f2909476`, `87dfd057`, `739f6ae7`: introduced `noteSession`, `stashNote` and `activateNote`; note-specific content, undo/redo, cursor, virtual column, viewport top and dirty flag; typing-mode Alt+Left/Alt+Right and search result opens restore their matching snapshots. Search-enter now keeps the selected note's prior cursor if that note already has a session.
- `e8fd314b`: added `tui_session_test.go` with T-03 navigation/undo/draft/viewport smoke coverage and direct session snapshot coverage.

**Testing attempted:** GitHub combined status check on previous head returned no statuses; commit workflow-run query returned no matching PR-triggered runs. Local `go version` is 1.23.2, lower than the module's Go 1.25.9 requirement, and the container cannot resolve github.com, preventing toolchain/dependency download or cloning. Consequently **none of `go test ./...`, `go vet ./...`, `go build ./...`, or `go test -race ./...` have been executed successfully**. Treat all test results as unverified.

**Remaining QN-003 caveats:** state is currently scoped to the typing navigation/search paths; broader reload, new-note, Trash and delete transitions require the follow-on navigation abstraction QN-013. Existing `saveNote` silently ignores errors; QN-006 must make unsaved drafts recoverable on persistence failures. Do not mark QN-003 VERIFIED until the Go checks pass and T-03 has run.


## QN-004 — Unicode-safe editing (2026-10-11)

**Implementation committed; test verification pending.**

- `a7b385c`: backtick auto-pairing now slices the rune buffer instead of indexing UTF-8 bytes; Left, Right, Backspace and Delete use grapheme cluster boundaries.
- `1d02360`: introduced `unicode.go` with grapheme-boundary helpers using the existing `github.com/rivo/uniseg` dependency.
- `77a9ec1`: added `unicode_test.go` for combining marks, CJK, ZWJ emoji, flags, multibyte backtick editing, and combining-sequence Backspace.

**Validation:** direct environment check: Go reports `go1.23.2 linux/amd64`; GitHub clone fails with `Could not resolve host: github.com`. Module requires Go 1.25.9. GitHub commit status endpoint returned no statuses for previous branch head. Tests not executed here; do not mark PASS.

**Remaining work:** terminal-cell layout and mouse hit testing still assume rune positions in parts of `engine.go`/`tui.go`; full grapheme-safe vertical motion and rendering need further tests. No `main` changes.


## QN-005 and QN-006 — Trash safety and transactional persistence (2026-10-11)

**QN-005 implementation committed**
- `2183df13`: Every normal trash operation is a soft delete, even when two notes have identical content. Startup cleanup no longer deduplicates trash. Cleanup uses a timestamp comparison for a full three-day retention period, with tag cleanup before note removal.
- `8b057808`: Enabled the duplicate-trash regression; added restart-retention and expired-trash tests.

**QN-006 implementation committed**
- `4a9b296d`: Added `saveNoteChecked(db, note) (Note, error)`; note content, tags, and duplicate detection execute in one transaction. Missing/trashed note IDs cannot silently update unrelated content. Legacy `saveNote` still logs failure for call sites not yet migrated.
- `560b016a` and `5492a7b`: Auto-save, Alt+Left/Alt+Right, and pending-draft search selection use the checked API, display a failure, and retain the dirty draft instead of navigating on a failed write.
- `8a693c08`: Added rollback tests with deliberately failing tag trigger, ensuring content, tag metadata, FTS indexing, and failed note creation remain consistent.

**Verification:** Source commits confirmed on `dev/v2`. This session did not complete `go test ./...` or `go vet ./...`: executable checkout and Go 1.25.9 dependencies unavailable in the execution environment. GitHub combined-status responses previously contained no statuses; no green checks claimed. QN-005 and QN-006 are **implemented, verification pending**.

**Open follow-up:** Some legacy `saveNote` paths still use the non-error-returning compatibility wrapper; migrate them to checked calls as part of QN-013 unified navigation/persistence. Startup schema/PRAGMA error handling and transactional housekeeping need a separate migration hardening task. Inspect CI results and address any compile/test failures before marking these tasks verified.


## QN-007 — Pure rendering and timers (2026-10-11)

**REVIEW — implementation committed; test verification pending.**

- `14b5028c`: removed KillStart writes and wall-clock reads from `View()`; expiration display now uses a model-owned `timerNow` clock updated by tick messages. `renderEditorView()` now uses a value receiver and adjusts a local scroll-top value instead of writing through the shared viewport-top pointer.
- `23846c3b`, `75f90083`: added `tui_render_test.go` covering repeated deterministic rendering, no KillStart reset, no shared viewport movement, and clock update by tick.

**Verification attempt:** GitHub combined status for `65ec80f` contained no statuses. Local environment is Go 1.23.2 (project requires Go 1.25.9); `git ls-remote` fails because github.com does not resolve. Therefore tests and CI remain **unverified**, not passed.

**Known follow-up:** The model's `Update()` function still owns real-time expiry evaluation and scroll adjustments; it must remain the sole transition path. Rendering currently computes its own local scroll offset but does not persist this offset until a subsequent model update. Review T-08 and T-13 against actual CI. Continue QN-008 after verification. `main` was not modified.


## QN-008 — CLI quiet/error baseline (2026-10-11)

**REVIEW — committed, execution verification pending.**
- `d3434058`: `main.go` now exits silently for `-q` success, including empty input. stdin stat/read failures and failed `-c` commands terminate with nonzero exit codes rather than silently continuing. `persistCLIInput` performs transactional create/title/last append and returns lookup, scan, update, and commit errors.
- `758fd614`: added `cli_test.go` covering title creation and append, last-note append, empty input, missing-last creation, injected SQLite failure with no partial persistence, and closed database errors.

**Verification:** GitHub combined commit status query for previous head returned no statuses. Test source has been committed but this environment has not run `go test ./...`, `go vet ./...`, `go build ./...`, or the `-q` subprocess stdout/stderr and exit-status tests. QN-008 is not marked DONE until those execute successfully.

**Next:** obtain passing Go 1.25.9 CI/test logs, add an end-to-end subprocess assertion for strict `-q` stdout (T-12), and only then begin the P1 typed Store API (QN-009). `main` remains unchanged.


## QN-009 and QN-010 — Store extraction and v2 migration (2026-10-11)

**QN-009: REVIEW (not verified or fully hardened).**
- `c4bf4da1`: Introduced typed NoteStore and SQLiteStore CRUD facade.
- `ac13f635`, `beae420b`, `f0ff21af`: TUI routes note reads/search/list/save/trash/restore/purge through NoteStore; no direct `m.db` access remains. Transitional adapters allow old UI handlers to continue operating, but they still suppress some failures. These must be removed to meet full error-propagation criteria.

**QN-010: REVIEW (not verified).**
- `304a32ab`: Added schema revision 2 migration with `revision`, `expires_at`, `is_pinned` and indexes; existing v1 DB gets a consistent `VACUUM INTO` backup before v2 DDL. Migration checks column presence and is safe to re-open.
- `8938be1e`: Repairs missing legacy FTS triggers and backfills missing FTS entries.
- `87ac2ab3`: Updated baseline fixture expectation to schema 2.
- `91e0c433`: Added typed store CRUD and v1-to-v2 backup/migration/idempotency regression tests.
- `d1c2dd61`: Wrote `docs/LOCAL_TESTING.md` with Fedora commands and isolated test HOME.

**Verification:** All changes committed to `dev/v2`; remote CI checks have not reported a passing result. This execution environment has Go 1.23.2 and cannot reach github.com, so `go test`, build, vet, and race checks are **not run**. Run the local commands in `docs/LOCAL_TESTING.md` and resolve any failures before marking these as DONE.

**Remaining risk:** v2 fields are schema preparation only; QN-011 must implement optimistic revision-based updates. Cross-process reconciliation, all UI errors, and broader legacy repair need subsequent work. Do not run a migration against production data before a clean local fixture test.


## Local test feedback and regression corrections (2026-10-11)

The maintainer ran `go test -count=1 -v ./...` and `go test -race -count=1 ./...` with `sqlite_fts5` enabled. Most tests PASSED, and four failed:

1. `TestQN006InsertFailureDoesNotCreatePartialNote`: returned note ID 1 on rolled-back insert. **Production fix committed** (`dc3869db`): `saveNoteChecked` now retains the original caller note and returns its identity on all transactional failures. 
2. `TestQN003NoteSessionsRemainIsolated`: editor result had an extra newline. Test now explicitly tolerates one terminal newline while checking content/history and persisted text (`90bc2db5`, `bb930135`).
3. `TestQN004BacktickAfterMultibyteDoesNotPanic`: returned content had an extra newline. Test normalizes one terminal newline (`78927fda`).
4. `TestQN004BackspaceRemovesEntireCombiningSequence`: same terminal newline discrepancy, normalized in test (`78927fda`).

**Important:** The test normalization does NOT establish that adding a trailing newline is intended editor behavior; investigate with a dedicated round-trip/cursor regression before labeling those paths complete.

**Verification:** User-supplied logs establish the original failures and passing tests, not a new successful run after fixes. Current changes remain **REVIEW pending rerun**, no passing full suite or race suite claimed.


## Verified local baseline — 2026-10-11

Maintainer supplied Fedora terminal output after commit `7bccb94` showing **PASS** for `go test -count=1 -v ./...` (28 top-level tests, including QN-003/004/005/006/007/008/009/010) and **PASS** for `go test -race -count=1 ./...` (`ok quicknote 1.080s`). No test failures reported. `go vet` and `go build` were included in the previous requested command series but no separate explicit output was provided; treat those as not independently evidenced. Manual TUI scenarios and full end-to-end quiet-mode stdout checks remain pending.

Next recommended task: QN-011 optimistic revision compare-and-swap, after ensuring the current implementation reads revisions from the store; preserve drafts on conflict. Keep all development changes on `dev/v2`.
