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

QN-029 review correction: original bracketed-paste events and non-key textinput callbacks are preserved; Custom input scrolls horizontally with visible cursor/tail after resize; a second confirmed TTL is retained in the existing lifecycle queue and applied at the first acknowledgment's revision. Three faithful red/green regressions, full/race/vet/build, original code-review reproductions, adapted spec reproduction and final bracketed-paste/focus PTY pass. See newest ledger/task-2 report for exact correction SHA/evidence. Re-review before integration; coordinator separately owns mixed historical/new timestamp ordering, which this correction does not change.

QN-029 correction round 2 completes pending exit/navigation after a queued TTL declines to start: save acknowledgments fall through to existing continuations, and lifecycle acknowledgments resume navigation only for the still-active matching pending-action owner. Actual key regressions cover edit/switch cancellation plus Esc, valid/canceled TTL plus Ctrl+N, another draft's pending navigation, and save failure/conflict preservation. Source guards and CAS bases remain intact. Original re-review overlays pass; exact final gates/SHA are in the newest ledger/task-2 report. No foundation timestamp-ordering changes or integration were performed by this task.

## M2 current continuation — QN-029 accepted (2026-10-13 UTC)

QN-028 foundation and BUG-14 chronology correction are accepted; QN-029 is accepted through b1494c5 after independent reviews and merged full/race/vet/normal-build/CLI/scripted TTL checks. Next QN-030 pinning, then QN-031 ghosts and QN-032 Trash. The approved M2 execution remains active; follow newest ledger evidence rather than historical REVIEW/next-task sections. Use the shared lifecycle CAS controller; completion must resume pending exit/navigation when a queued operation is canceled, and only the matching owner may resume navigation. Main remains protected; schema3.

## M2 QN-030 worker handoff — 2026-10-13 UTC

QN-030 is implemented in isolated `qn-v2/QN-030-pinning` from `f48a32c`, **REVIEW** pending coordinator's independent reviews/integration/publication. Its implementation commit includes the newest progress entry; exact SHA/report is `.superpowers/sdd/2026-10-12-m2-lifecycle/task-3-report.md` in the coordinator workspace. Pin/Unpin uses existing lifecycle CAS/save serialization, default Cancel and explicit no-timer unpin confirmation. Pin clears expiration and observes directive atomically; unchanged directive remains canceled after unpin/restart/save. Pinned directive edits do not leave hidden deadlines. Commands titles reflect pin state; ghosts/Trash/unsaved drafts have disabled explanations. Search summaries retain preview and expose `[Pinned]`; no sidebar/order/filter work.

Red/green pin UI and preview-loss regressions, real-store CAS/failure/raced draft ownership, queued source cancellation/exit, default/legacy/custom keyboard, six-size model layouts and focused default/legacy scripted PTY checks are in newest ledger/report. Full/race/vet/build fallback and exact final outputs are recorded there. Normal isolated build hit VCS stamping; default cache hit read-only writes, so writable `/tmp/qn030-go-cache` and documented `-buildvcs=false` were used for affected gates. Coordinator explicitly approved only the required `tui_async.go`, `overlays.go` and `keymap.go` routing/title seam extensions. DB schema3, synthetic fixtures only; no main/real-data/integration/push. Coordinator owns DONE. Next QN-031 after acceptance, then032; approved M2 continues.

QN-030 correction round 1 addresses both independent Important findings: Commands now displays clear-TTL/no-timer consequences in task-owned Pin/Unpin titles and keeps its selected row visible at 25×5; a held matching-generation search result reconciles newer known lifecycle metadata while preserving incoming preview/match/content fields. New pin **and unpin** red/green regressions verify this race and subsequent latest full load/CAS pairing. Existing full-load/refetch/save logic is unchanged. Full/race/vet/build fallback, 20 focused repetitions, original review overlay and default/legacy six-size snapshots passed; exact final PTY/SHA is in newest ledger/task-3-report. Additional incoming-search/task-only short-panel seams explicitly approved by coordinator. REVIEW pending acceptance; no integration/push or other QN scope. Next031 after acceptance.

## M2 current continuation — QN-030 accepted (2026-10-13 UTC)

QN-030 is accepted on dev/v2 as808b6a8+b4d606f, with both independent reviews and full/race/vet/normal-build/keyboard evidence in the living ledger. Next QN-031 ghosts, then QN-032 Trash; finish the approved milestone. All VC operations now use GitButler per user instructions. InstalledCLI does not support linked worktrees; preserve existing workers and use the coordinator primary checkout for GitButler integration. Verify the actual GitButler target before any land/pull; never use main. Reviewed history and pre-existing untracked files stay intact.

## QN-031 worker handoff — 2026-10-13 UTC

QN-031 is **REVIEW** on `qn-v2/QN-031-ghosts`, base `22606be95e5307a31d745d4ddb49c80339515b2a`; this entry ships with its implementation. Exact immutable SHA and gate report are in coordinator `.superpowers/sdd/2026-10-12-m2-lifecycle/task-4-report.md`. GitButler's linked-worktree limitation required coordinator-assigned exclusive primary-checkout implementation on a dedicated branch. Pre-existing untracked user files and all reviewed history remain untouched. Coordinator owns independent review/DONE/integration/publication.

New Ghost is Commands/Alt+G in default and legacy profiles (custom action `ghost.new`). Ctrl+G opens default-Cancel conversion; Tab/Left/Right cycles Duplicate keep original / explicit Trash original with 72-hour recovery. Ghost-to-persisted conversion separately confirms, including an empty note; failed creation retains explicit retry authorization in EditorDraft. Copy confirms before clipboard invocation; export confirms after filename submission, before external write. Confirmed output does not persist the ghost in SQLite. Ctrl+D/Commands Discard ghost drops content/history/pending-preview references; obsolete export checks cannot write. `#kill`, autosave, promote and maintenance cannot persist a ghost. README documents intentional compatibility and privacy limits.

Shared seams: `ghost.go` owns newGhostDraft/requestGhostConversion/discardGhost. `lifecycleIntent.GuardGhost/GhostContent` extends the existing queue; kind `ghost` validates the original full snapshot/revision and `ghostTrash` calls `TrashSnapshotCAS`. Dirty source saves finish first, source edits/switches cancel queued conversion, and started completions preserve both captured ghost and newest source history. Pending keys are removed when an unsaved source acquires an ID. Successful late Trash followed by a newer observed restore retains the captured ghost without regressing restore state. QN-032 should record ID/revision/recovery-bound-only undo metadata for successful `ghostTrash` as well as ordinary manual Trash in receiveLifecycle. Do not persist a second source content copy in TrashUndo.

Final full17.881s/race25.138s/vet/normal-primary-build passed; 20x ghost/QN017 acceptance23.749s; default/legacy/custom six-size snapshots, synthetic SQLite/tags/FTS/files/log sentinel audit, clipboard shim, actual export/Cancel/stale checks, failed-empty-persistence retry and exact72h same-ID recovery passed. Final rebuilt PTY evidence is in the newest ledger/report. Schema3; no production backup/migration/real notes/main/push. GitButler scoped diffs plus whitespace inspection replace git diff --check by current user policy. Human Fedora/tmux/clipboard, remote CI and release benchmarks remain NOT RUN. Next QN-032 after coordinator acceptance; continue approved M2.

### QN-031 review correction — 2026-10-13 UTC

Public navigation during a dirty source's conversion save now cancels the unstarted ghost intent and resumes after the ordinary save; an unsaved source's pending owner is remapped before chained saves. Already-started successful conversion keeps its captured ghost inactive while a public transition/load is pending, preserves newest source/history, and finishes matching navigation. Dirty Trash recovery still blocks exit and unavailable navigation. This is a separate correction above reviewed `abfe7bb306fa585ae30fc35074e5dc8f795211cf`, without history rewrites; exact correction SHA is in task-4-report.md. Forty-one new public-key cases and the original independent review reproduction passed. Final full/race/vet/normal primary build, 20x focused repetitions, layout checks and default/legacy synthetic PTY (4 held-SQLite-save navigation cases plus 8 task privacy cases) passed; exact commands/results and read-only cache workaround are in the newest ledger entry. Status remains REVIEW for coordinator re-review; schema3, no real-data/main/push/integration changes. QN-032 follows acceptance and must record successful manual `ghostTrash` acknowledgments whether their ghost activates or remains inactive.

### QN-031 review correction 2 — 2026-10-13 UTC

The second correction resolves unavailable pending arrow keys before adding the captured ghost to the list, preserving the original navigation boundary for both Keep and Trash (including Keep's subsequent source save). Available neighbor identities and source history remain intact; right/left boundary tests assert active source, one inactive captured ghost, matching save/recovery state and cleared continuation. Separate commit above unchanged reviewed `ef8e3e2e3c0efd5b18d10aecd1f79c74b3055ed1`; exact SHA in task-4-report.md. Full18.004s/race25.372s/vet/normal build/repetition26.448s/layout and synthetic PTY4+8 passed, with independent red/green reproduction recorded in the ledger. REVIEW for coordinator acceptance, schema3, no new shared API, main/real-data/push/integration changes. Continue QN-032 after acceptance using the existing successful `ghostTrash` acknowledgment hook.

## Current continuation — QN-031 accepted, QN-032 next

QN-031 is accepted on dev/v2 as9bd2630+76379fe+f2bbf919 after independent spec/code reviews and two ordering corrections. Final full/race/vet/normal-build, 20x repetitions, 44 public-key cases and 12 synthetic PTY cases passed; exact evidence and source/integrated SHAs are in the living ledger. Continue QN-032, then final M2 acceptance. Preserve newer source recovery and inactive captured ghosts; record metadata-only undo for successful manual ghostTrash as well as ordinary Trash. No production data was tested.

GitButler-only continuation: one product writer on an anchored dedicated branch in the primary checkout; linked worktrees unsupported. Use `but branch new <agent-branch> --above dev/v2`, then reviewed explicit commit picks after `but setup` and `but teardown --checkout-to dev/v2`. Target metadata is still origin/main; never land/pull against it. Explicit authorized dev pushes include stack ancestors: a previous push unexpectedly created origin/qn-v2/QN-028-durable-ttl869a7d0 despite a dev-only dry run. The ref remains, contains existing dev commits and is documented in the ledger; no safe supported cleanup was found. Main remains protected.

## QN-032 worker handoff — 2026-10-13 UTC

QN-032 is **REVIEW** on `qn-v2/QN-032-trash` from `b38b0d6bdde52772257b8e0981d8f92b3bfed72a`. Implementation SHA and exact final gates are recorded in `.superpowers/sdd/2026-10-12-m2-lifecycle/task-5-report.md` and the newest living-ledger entry. Coordinator owns independent review/integration/publication and final M2 acceptance. No main changes or push; pre-existing user files remain unmodified/uncommitted. Inspection-only accidental config/log diff inclusion is disclosed in the ledger; all subsequent But inspections target individual owned paths.

`trash_ui.go` supplies read-only scrollable metadata/content preview, Ctrl+R Cancel/no-timer/validated-directive-reset choices, Commands Undo last move to Trash and confirmed Delete forever. Reuse the existing confirmation/modal/save/lifecycle controller. `TrashUndo` holds only ID/revision/recovery deadline; successful ordinary and ghostTrash results record it before late-success reconciliation. Request generations prevent an older delayed acknowledgment from replacing the latest target, including clock corrections. Undo preserves ID/history, guards remote restore/edit/purge, clears expiry, and navigates only from an unchanged clean initiator. Ctrl+Z is still text undo. Delete forever calls trash-only `DeleteTrashCAS` and returns to Trash; errors retain selection. Ordinary persisted empty notes soft-delete; ID-zero/ghost discard share reference cleanup and do not create Trash. In-flight first saves are acknowledged/remapped before soft deletion.

New store guard allows exactly72h and refuses later restore even before maintenance purges; malformed legacy deletion timestamps stay recoverable. Metadata mutations use actual action clock (`ttlNow`, injected Clock or RealClock), preserving pure captured-time View. Strong regressions cover early reset/manual deletion timestamps, stale/late ACKs, readonly scrolling/search, pinned/no-rearm, duplicates/history/FTS/rollback/CAS, and visible selected Trash actions at25×5 in default/legacy/custom profiles. Schema remains3; no production database access/test/backup/migration. Human platform/clipboard, remote CI and benchmarks remain NOT RUN. Continue independent reviews/final M2 gates; start QN-018 only after milestone acceptance.

### QN-032 correction 1 awaiting re-review — 2026-10-13 UTC

Separate correction above unchanged reviewed `2bd7e6c3757acac980e59a047296aafa43cb54fc` on `qn-v2/QN-032-trash`; immutable correction SHA in task-5-report.md. Both review findings reproduced and corrected. Trash stores the actual last-viewport bound shared with pure rendering, including Unicode, tiny sizes and return from a resized Commands overlay. Short previews remain at zero. `layout.go` now exposes shared panel geometry with equivalent existing rendering. `loadRequest.PurgedSourceRevision` carries only successful deletion metadata through follow-up loads and full refetches; unchanged clean source cleanup survives maintenance snapshots while newer dirty/version/revision drafts and error recovery retain history.

Full23.390s/race97.907s/vet/normal primary build/20×110.274s/layout5.078s and synthetic PTY6/6 passed; final reviewer-adapted regression2.268s passed. Exact baseline/red/green/gates are in the ledger/report. The reviewer replaced its insufficient short scroll fixture independently with long Unicode immediate-reversal plus short-zero cases, preserving the original overlay for provenance. Schema3; no data/schema/controller expansion. Human platform/real clipboard, remote CI and benchmarks NOT RUN. No production data or pre-existing user-file inspection/mutation in this correction; prior inspection deviation remains disclosed. REVIEW pending coordinator's scoped re-review/integration/final M2 acceptance; no push or history rewrite. QN-018 follows only after M2 acceptance.
