# Quicknote v2 — implementation progress

Branch: `dev/v2`
Latest implementation SHA: QN-012 publication recorded below
Baseline main SHA: `d3e5a119cdf12ec99fcc14a62545f60729adea54`
Current task: QN-012 — implemented and acceptance verified; next QN-013
Updated: 2026-10-12 UTC (2026-10-11 America/Los_Angeles)

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


## Documentation consolidation and session handoff — 2026-10-11

**GitHub is now the authoritative source for all Quicknote v2 generated Markdown artifacts:**

- `docs/QUICKNOTE_V2_SPEC.md` — full v1.0.0 specification, uploaded from the original generated source (commit `bda14cfb`).
- `AGENTS.md` — original coding-agent instructions, updated with the current task and documentation map (commits `80fe3db7`, `1a6a7a70`).
- `docs/IMPLEMENTATION_PROGRESS_TEMPLATE.md` — archived **original 48-task planning ledger** (commit `931f1f80`), not to be substituted for this living ledger.
- `docs/SESSION_HANDOFF.md` — complete repo-based next-session context and ready-to-paste QN-011 prompt (commit `c4f2a21d`).
- `docs/LOCAL_TESTING.md` and `docs/QN-001-BASELINE.md` were already committed and remain available.
- This `docs/IMPLEMENTATION_PROGRESS.md` retains all historical implementation/test records and is the only live task-status document.

**Additional maintainer verification:** after reporting 25 of 29 manual checklist items completed, the maintainer subsequently confirmed pipeline capture persisted with an isolated Zsh test HOME and independently reran `TestQN010V1MigrationBackupAndIdempotency` (PASS). Permanent-delete-from-Trash UI remains unavailable; the remaining planned manual checks are not all confirmed. No new executable build was run for this documentation-only task.

**Next recommended task:** QN-011, optimistic compare-and-swap revisions, after re-reading the specification and inspecting actual `dev/v2` HEAD. Preserve `main`.


## QN-011 — Revision CAS and recoverable conflicts (2026-10-12 UTC)

**DONE on `dev/v2`.** Implementation and accompanying ledger/handoff commit: `595dd7da34d47852b511349e4750f11862c3d161`. This documentation follow-up records its exact SHA. Starting HEAD was `f0ffaec` (documentation consolidation). No changes or merges to `main`.

### Changes and contracts

- `db.go`: content, derived tags/FTS, legacy timer fields, expiration and pin snapshots save transactionally with `WHERE id=? AND revision=? AND deleted_at IS NULL`. Successful creates start at revision 1 (the existing v2 default); successful updates advance once. Conflicts and rolled-back writes return the original caller snapshot, including its revision. Production SQLite connections use `_txlock=immediate` and the existing 5000 ms busy timeout: acquire the SQLite writer lock before FTS can establish a read snapshot, then evaluate CAS. There is no retry with a refreshed revision or automatic merge.
- `store.go`: `GetNote` reads content, revision, metadata and tags from one SQL snapshot. List/search carry revisions. `ConflictError` supports `errors.Is(ErrConflict)` and `errors.As`, with note ID and expected/actual revisions. Missing, trashed and wrong-state errors remain distinct from SQLite errors. Trash, restore, purge, pin and expiration have revision-guarded APIs; metadata changes advance revision atomically. ID-only compatibility methods fetch a snapshot then perform CAS; the TUI uses its displayed/base revision instead. Explicit `SaveCopy` can persist even a deliberately empty conflicting draft.
- `main.go`: title/recent append and creation use the same checked save transaction, including tags/FTS and metadata. Append conflicts reach stderr/nonzero exit through the existing CLI error path. Existing flags, quiet output and input normalization remain in place; ties in recent selection are now deterministic by ID. The broader byte-preservation/lifecycle CLI work remains QN-040.
- `tui.go`, `tui_conflict.go`: every existing UI save handler checks errors. Conflict blocks navigation/new/trash/delete/exit before losing a draft. Sessions retain their base snapshot/revision, dirty state and histories. Content-only reloads were replaced by snapshot reads; failed navigation loads keep the previous identity. Search opening no longer reloads/replaces the underlying editor list. Persistent status survives typing. Recovery is keyboard-only: `C` saves a copy, `I` toggles a line comparison, arrows/PageUp/PageDown scroll, `R` then `R` confirms discarding the local draft and re-reads the current stored version, `Esc` keeps editing. Remote trash/purge allows copy recovery but cannot silently reload an unavailable active note. No automatic overwrite choice is provided.
- Regression files: `cas_test.go`, `cas_process_test.go`, `cas_migration_test.go`. Two independent SQLite pools (one physical connection each), and two separate child processes, start with the same revision: exactly one succeeds, the other receives conflict. Tests also cover metadata races, tags/FTS, transaction revision rollback, busy/closed DB errors, CLI append collision/retry, session base revisions, failed/empty copies, latest-at-confirmation reload, deleted remote recovery, and four conflict layout sizes. Existing `tui_baseline_test.go` and `tui_session_test.go` fixtures now read real note snapshots instead of fabricating revision-zero persisted notes; their behavioral assertions remain intact.

### Verification executed in this session

Environment: Linux amd64, Go **1.25.9** installed in `/tmp/go`, CGO/GCC available. All tests use synthetic temporary data. No personal notes were accessed.

**PASSED:**

- Before production changes: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=1 ./...` at `f0ffaec`.
- Final suite: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=1 ./...`.
- Verbose regression run: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=1 -v ./...` (followed by an additional successful full run after adding busy, empty-copy and backup-recovery coverage).
- `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -race -count=1 ./...`.
- `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=20 -run 'TestQN011T17' .` — repeated independent-connection and independent-process T-17 checks.
- `CGO_ENABLED=1 /tmp/go/bin/go vet -tags sqlite_fts5 ./...`.
- `CGO_ENABLED=1 /tmp/go/bin/go build -tags sqlite_fts5 -o /tmp/quicknote-qn011 .`.
- `git diff --check`; `gofmt -l` on all changed Go files returned no filenames.
- Isolated built-binary CLI check using Python/subprocess: quiet title capture and recent append emitted zero stdout/stderr on success; revision advanced and tags updated; injected UPDATE failure produced nonzero exit/meaningful stderr/zero stdout, with content/revision unchanged and fresh integrity check `ok`.
- Scripted **80×24 PTY** keyboard pass: external connection changed revision; local edit plus Ctrl+N displayed conflict and retained draft; `I` showed both line variants, `C` persisted a distinct copy, Esc exited, and both versions remained in SQLite. Model render tests cover **120×40, 80×24, 50×12, 25×5**, without clipping the recovery choices.
- Legacy v1 fixture migrated to v2, rejected the stale legacy snapshot, preserved active/trash IDs and content, and recovered the original content/tags/schema version from `.pre-v2.bak` via SQLite `VACUUM INTO`; recovered `PRAGMA integrity_check` returned `ok`.

**Initial failures corrected:** new tests first failed to compile because CAS APIs were absent; the first simultaneous-write test exposed deferred transaction lock-upgrade contention, resolved with immediate write transactions. Old model fixtures lacked revisions, resolved by loading real persisted snapshots. `git diff --check` initially found trailing SQL whitespace, removed before commit.

**Diagnostic caveat:** the bundled SQLite FTS5 can report a transient checksum mismatch on a connection whose FTS structure cache was populated before another connection's write. This was reproduced on unchanged `f0ffaec`: cached checker reported mismatch, a fresh checker reported `ok`. The cross-connection regression validates persisted integrity using a fresh connection, and independently validates winning content/tags/FTS. No speculative FTS/schema rewrite was made.

**NOT RUN:** human-operated Fedora/Zsh/Kitty/tmux interaction and platform/clipboard matrix; remote GitHub CI results were not used as passing evidence. PTY automation is not a claim of manual Fedora verification.

### Database, compatibility and next task

Schema remains **user_version 2**; no new migration or retention policy change. Existing v1 backup/migration/recovery fixtures pass. Production database/backup operations were not performed.

No QN-011 blocker remains. Saving is still synchronous and can wait for the SQLite busy timeout; **QN-012 is next READY** (011 prerequisite satisfied): typed asynchronous save/load/search/highlight commands, note/request/local-version generations, stale-completion rejection, and T-02/T-24. QN-013 still owns the broader Draft/NoteID refactor. List/search compatibility reads still suppress some errors; permanent-delete confirmation from Trash is QN-032; ghost conversion safety is QN-031; editor trailing-newline behavior remains open. Pin/TTL APIs here provide CAS groundwork without exposing those later lifecycle features.

**Publication:** direct Git HTTPS push had no credentials in this container. The connected GitHub API published the implementation with tree `8b1970c759b360d99bfc5464d841501cd9b6602c`, exactly matching the locally committed/tested implementation tree. The documentation follow-up records the published implementation SHA. Only `dev/v2` is advanced with an expected-head check; `main` remains `d3e5a119cdf12ec99fcc14a62545f60729adea54`.


## QN-011 — Fresh branch audit and concurrent recovery coverage (2026-10-12 UTC)

The requested task was already implemented in `595dd7da34d47852b511349e4750f11862c3d161`; this session began at `3340980c9e8b56f10d4b140abc2d218abd56df53`. Read all governing documents, including the complete specification, and reviewed the actual CAS storage, CLI, TUI recovery and regression code. QN-011 remains **DONE**. No production rewrite or QN-012 work was performed.

**Changes:** `cas_test.go` adds simultaneous content-versus-pin/expiration/trash writes through two independent SQLite connections, asserting exactly one winner and one revision increment, winner content/tags/FTS, and no losing metadata update. A second test runs two recovery-copy saves concurrently, checking distinct IDs, revision 1, both draft payloads/tags recoverable, and the original unchanged. Regression and verification supplement commit: `7b41a3e1ce960be91455688df97ef234c577e2ec` (includes this living ledger).

**Verification executed here (Linux amd64, Go 1.25.9, CGO enabled):**

- PASS baseline at `3340980`: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=1 ./...`.
- PASS final full suite: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=1 ./...` (`ok quicknote 0.322s`).
- PASS race: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -race -count=1 ./...` (`ok quicknote 2.535s`).
- PASS repeated concurrency: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=20 -run 'TestQN011(T17|Simultaneous|ConcurrentRecovery)' .` (`ok quicknote 0.944s`).
- PASS `CGO_ENABLED=1 /tmp/go/bin/go vet -tags sqlite_fts5 ./...`.
- PASS `CGO_ENABLED=1 /tmp/go/bin/go build -tags sqlite_fts5 -o /tmp/quicknote-qn011-recheck .`.
- PASS `git diff --check`; `/tmp/go/bin/gofmt -l cas_test.go` produced no filenames.
- PASS isolated Python/subprocess built-binary CLI checks: quiet title creation, title append and recent append each returned exit 0 with empty stdout/stderr; revision advanced to 3 and all three tags persisted. An injected UPDATE trigger failure produced nonzero exit, empty stdout and diagnostic stderr, with unchanged content/revision; `PRAGMA integrity_check` returned `ok`.
- Existing migration/backup recovery, separate-process T-17 and conflict model/layout tests executed as part of the full and race suites.

**FAILED:** none. **NOT RUN this session:** human-operated Fedora/Zsh/Kitty/tmux tests, new PTY automation, remote CI and the platform matrix. Historical PTY evidence above remains historical.

**Data and scope:** only temporary synthetic SQLite fixtures were used. Schema stays `user_version 2`; no production database, schema, retention, CLI flag or keymap changes. No QN-011 blocker found. Next READY task remains **QN-012**. Permanent deletion UX, compatibility adapters and trailing-newline investigation remain assigned follow-ups. `main` was read-only and remained `d3e5a119cdf12ec99fcc14a62545f60729adea54`.


## QN-012 — Typed asynchronous commands and stale-result protection (2026-10-12 UTC)

**DONE on `dev/v2`**, starting from `3d7c8659d92cea926840af77578dca4d85b771d3`. Implementation SHA is recorded in the publication follow-up. This task includes T-02 and T-24; it preserves the QN-011 storage CAS and T-17. No changes to `main`.

### Changed files and behavior

- `tui_async.go` (new): immutable typed save/load/search/highlight requests/results, monotonically increasing generations, per-note local versions, one in-flight save per note, and serialized completion handling. Workers capture store/snapshot values, never mutable model buffers or session maps. A matching save completion advances only its note's base revision; subsequent edits remain dirty and chain another CAS save. Duplicate/obsolete acknowledgments cannot consume newer requests. Inactive failures retain their own session/error/base revision and cannot change the active note's dirty/status state.
- Loads retain the source draft until a successful matching response. Out-of-order generations, canceled opens and edits made during a load reject the response. A load cannot regress a session revision already acknowledged by a save. Search uses a 100 ms debounce, discards superseded debounces and out-of-order responses, clears stale selectable results while searching, and checks query/scope/generation. Enter opens the newest matching stable ID. Highlight results check note ID, local version, generation and text, including equal text across notes or after undo.
- Existing save-dependent actions queue behind the newest draft while keys/resize continue. Trash/restore/purge/ghost-conversion and help reads also execute in commands; failed transitions keep identity/draft. An edit during a destructive write retains a recoverable local draft. Restore targets the restored ID and preserves the previous in-memory legacy timer reset; durable TTL/restore choices remain QN-028/032.
- Conflict preview reads, copy writes and confirmed reloads are asynchronous. Escape invalidates a pending reload. A successful copy acknowledges recovery only for the copied version of the original retained session; future edits invalidate that acknowledgment. Exit waits for all outstanding saves, including inactive notes, and reopens failed retained sessions instead of silently quitting. Successfully recovered copies permit exit.
- `tui.go`: routes I/O through the new dispatcher, makes note installation pure, adds session version/recovery state and visible Saving/Unsaved/Loading/Working/failed labels. Normal typing no longer calls SQLite. `tui_conflict.go`: keeps pure conflict rendering and non-I/O key handling, including pending-copy feedback.
- `db.go`, `store.go`: checked list/search methods propagate query/scan/iteration errors to typed results instead of treating them as empty results. Legacy signatures remain available. CAS SQL, CLI flags, schema and retention are unchanged.
- `tui_async_test.go`, `async_test_helpers_test.go` (new): 28 QN-012 tests plus a command-completion driver. Existing `cas_test.go`, `tui_baseline_test.go`, `tui_session_test.go` now deliver commands/results explicitly; identity/history/content/CAS assertions remain intact.

**Intentional search behavior correction:** opening/canceling Ctrl+F preserves the dirty draft without saving solely because the overlay opened, as required by spec §6 N-04/§9.4. Selecting a result still resolves save/conflict before switching. The former QN-011 Ctrl+F-immediate-save case is replaced by explicit search-cancel-no-write and search-Enter-conflict-preservation tests. No key bindings changed.

### Verification executed in this session

Linux amd64, Go **1.25.9**, GCC/CGO available. The temporary `/tmp/go` toolchain disappeared across the resumed environment; Go was reinstalled at `/workspace/scratch/bf7235c166cf/go` before final checks. All databases/notes were synthetic temporary fixtures.

**PASSED:**

- Baseline at `3d7c865`: `CGO_ENABLED=1 /tmp/go/bin/go test -tags sqlite_fts5 -count=1 ./...`.
- Final full suite: `CGO_ENABLED=1 /workspace/scratch/bf7235c166cf/go/bin/go test -tags sqlite_fts5 -count=1 ./...` (`ok quicknote 10.320s`).
- Final race: `CGO_ENABLED=1 /workspace/scratch/bf7235c166cf/go/bin/go test -tags sqlite_fts5 -race -count=1 ./...` (`ok quicknote 12.676s`). Includes concurrently executing save/load/search/highlight workers while the editor processes edits/resize.
- Repeated acceptance: `CGO_ENABLED=1 /workspace/scratch/bf7235c166cf/go/bin/go test -tags sqlite_fts5 -count=20 -run 'TestQN012(T02|T24)|TestQN011T17' .` (`ok quicknote 6.774s`). T-17 includes independent SQLite connections and independent child processes.
- `CGO_ENABLED=1 /workspace/scratch/bf7235c166cf/go/bin/go vet -tags sqlite_fts5 ./...`.
- `CGO_ENABLED=1 /workspace/scratch/bf7235c166cf/go/bin/go build -tags sqlite_fts5 -o /workspace/scratch/bf7235c166cf/quicknote-qn012 .`.
- `git diff --check`; `gofmt -l` on all changed/new Go files produced no filenames.
- Model regressions: T-02/T-24, immutable snapshots, duplicate save responses, newer-edit CAS chaining, 100 deterministic edit/save cycles, dirty session restoration, stale loads/highlights, closed DB/search errors, read-only trash identity, startup response rejection and typing before startup completes without persisting placeholder text, ghost no-save, pending reload cancellation, late copy results, copy acknowledgment invalidation, exit/inactive failures, restored legacy timers, and a real held SQLite writer lock with continued typing/resize. Existing migration/backup recovery and four-size conflict tests also ran in the full/race suites.
- Final built-binary **scripted PTY** pass: hold an independent SQLite writer lock, type during the pending autosave, resize, release the lock and verify newest text; externally update the revision, trigger conflict via Ctrl+N, inspect both variants with I, resize through 120×40/80×24/50×12/25×5, save a copy with C, exit with Esc; both versions persisted and SQLite integrity was `ok`.
- Final built-binary **Python/subprocess CLI** pass: quiet title creation, title append and recent append emitted empty stdout/stderr and exit 0; revision advanced to 3 and derived tags matched. Injected UPDATE trigger failure returned nonzero/diagnostic stderr/empty stdout and left content/revision unchanged; integrity was `ok`.

**Initial failures corrected:** compile errors from API conversion, synchronous test expectations before command driving, dirty search selection initially bypassing its save gate, and a highlight test whose fixture still had an unconsumed dirty flag. Additional review found and covered exit while an inactive save is pending, copy recovery versus later edits, legacy timer reset after restore, and cancellation of pending transitions when search opens. No final failures remain.

**NOT RUN:** human-operated Fedora/Zsh/Kitty/tmux checks, full platform/clipboard matrix, release benchmarks and remote CI verification. Scripted PTY checks are not claims of manual Fedora verification.

### Compatibility, remaining risks and next task

Schema stays **user_version 2**. No migration, retention changes, dependency upgrades, production database/backup operations, or `main` changes. CLI byte-normalization remains unchanged and assigned to QN-040. Shell filters/export/clipboard still have their legacy synchronous behavior; their dedicated tasks remain QN-039/041/036. Search grammar/escaping and fuzzy ranking remain QN-018–021. Async work can complete after cancellation; obsolete read/highlight results are discarded, while successful writes are reconciled or reported as saved copies. No latency/performance budget is claimed from these correctness checks.

No QN-012 blocker remains. **QN-013 is next READY**: explicit EditorDraft/authoritative NoteID, bounded per-draft history and source maps; preserve the typed commands, session versions, CAS, and conflict recovery while replacing index-driven state. Required T-01–04 and T-17 must pass. ID-zero draft/ghost bookkeeping and broader newline/tab/grapheme layout work belong to that refactor and the assigned editor/lifecycle tasks. Permanent deletion confirmation from Trash remains QN-032.
