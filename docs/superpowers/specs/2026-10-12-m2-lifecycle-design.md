# M2 lifecycle design and proposed specification amendment

Status: DRAFT for maintainer review. Planning only; no product code or database changes.
Baseline: `0778085c5815d56a439550ccd9e898fa79935dc1`, `dev/v2`, inspected 2026-10-12.
Authority: `docs/QUICKNOTE_V2_SPEC.md` v1.0.0, §§3–5, 7–8, 10, 12, 14 (QN-028–032).
Execution plan: `../plans/2026-10-12-m2-lifecycle.md`.

## Intent and scope

Complete all M2 lifecycle behavior: durable expiration, its picker/countdown, pinning, ghost creation and safe conversion, and recoverable Trash with permanent-delete confirmation. Preserve instant typing, exact text, stable IDs, per-draft history, optimistic concurrency, asynchronous generations and quiet CLI stdout. Each QN task remains independently committed and testable. Future agents implement after maintainer approval of this plan/design.

Use the current top-level Go package and existing draft/action/keymap/layout/overlay abstractions. Add focused lifecycle files; do not reorganize the repository into packages. No new dependencies are planned. The full fuzzy palette, sidebar, settings UI, selection, async shell runner, enhanced CLI commands and general backup/export product are outside M2.

## Current discrepancies requiring explicit resolution

The checked-out schema is v2 but only prepares `expires_at`/`is_pinned`. Timer ownership, cancellation and deletion reason are absent. The active editor reparses/restarts legacy timers on ticks; `RestoreCAS` rearms them; ghost conversion calls `PurgeCAS`; and empty active notes have a hard-delete shortcut. Startup cleanup uses `<=` at 72 hours, whereas §4.2 requires strictly older than 72 hours. Existing tests pin some transitional behavior; update them only with the documented lifecycle contract and new replacement regressions.

The specification requests pinning sidebar support before the sidebar's own QN-024 dependencies. M2 will supply metadata/action hooks and current Commands access; QN-024 builds the sidebar. Similarly, QN-029 uses today's Commands menu instead of implementing the QN-023 fuzzy palette. Its TTL status click gets a small shared status-region function, not a new general mouse system.

INV-11 says ghost content never deliberately touches clipboard/backups, while §4.4 explicitly permits a warned ghost export. The proposal below clarifies this conflict instead of silently choosing a rule.

## Proposed QN-V2 v1.1.0 amendment (not yet applied to the master spec)

1. Keep the historical v2 migration contract and task evidence. Add a transactional **database v3** migration for lifecycle metadata; never add fields to deployed v2 without increasing `user_version`.
2. Add `expiration_source TEXT NOT NULL DEFAULT 'none'` (values `none`, `directive`, `preset`), `expiration_needs_review INTEGER NOT NULL DEFAULT 0` (0/1), and `delete_reason TEXT NOT NULL DEFAULT ''` (empty for active/unknown legacy reason, `manual`/`expired` for new deletions). Reuse `kill_hash` as the observed effective-directive fingerprint; keep `kill_start` for verified legacy import/compatibility, not as an independent countdown authority.
3. Store new lifecycle/deletion timestamps as UTC **fixed-width millisecond RFC3339**, layout `2006-01-02T15:04:05.000Z`. Parse legacy SQLite timestamp/RFC3339 forms at the boundary. Normalize valid existing `expires_at` strings transactionally to this format before indexed comparisons, rounding upward to avoid early expiry. Preserve malformed expiry text, flag review, and exclude it from automatic expiry. New deadline instants are rounded upward to the next millisecond so rounding cannot cause early expiry. Historical created/updated/deleted timestamp bytes are retained during migration.
4. For a persisted ghost conversion, default to **duplicate into a new ghost and keep the original**. An optional explicit **move original to Trash and continue as ghost** uses ordinary 72-hour recovery; never hard-delete the original in conversion. Warn that persisted copies/backups may remain. A ghost can become persisted only after explicit confirmation.
5. Clarify INV-11: ghost content has no automatic persistence, indexing, copying, logging, backups or external export. Explicit user-requested export/copy may occur only after a warning and confirmation that content leaves memory-only mode. The internal ghost remains unpersisted. Existing shell filtering is explicit and must not log ghost content; no security guarantee about shell programs, terminal history or OS memory is implied.
6. Delete any persisted active note, including an empty one, through normal recoverable Trash. Permanent deletion from the Trash interface checks both revision and trashed state. The general legacy `PurgeCAS` adapter remains available to existing internal callers, but UI lifecycle actions never invoke it on an active original.

Approval of M2 implementation includes approving these changes. At execution start, amend the master spec to v1.1.0 with a dated amendment section and decision-log entries before using these semantics. If approval excludes an item, reconcile the design first; do not improvise a different policy in implementation.

## Lifecycle design

### Expiration and directives

One injected `Clock.Now()` supplies maintenance, mutations and tests. View only consumes the last clock message and persisted `expires_at`. Use one safe parser for `#kill`; select the last valid effective directive outside code/comments. Existing duration aliases, fractional fixed durations and legacy month=30-day duration remain compatible. Reject nonpositive, nonfinite, unknown or overflowing durations. Invalid-only directives/invalid legacy starts preserve the note and show review; a valid earlier directive still wins over a malformed later one.

A fingerprint identifies the effective directive after normalized case/spacing. Record it even when a preset, pin or restore cancels/overrides the timer. An unchanged directive never rearms after saves, restarts, unpin or restore. A changed/new valid directive sets an absolute deadline; removing it clears only a directive-owned deadline. Preset/Never updates source plus the observed fingerprint atomically, so the last explicit user action wins. Pin suppresses expiry and clears the deadline; edits to directives while pinned update the observation without creating a hidden deadline. Applying a finite preset to a pinned note is rejected with an explanation to unpin first.

Migration imports legacy active, unpinned directives only when the old hash matches the legacy parser, `kill_start` is valid, and the duration/deadline is safe. A stored valid absolute deadline takes priority over legacy inference. Trashed notes are never expired during import; historical directives remain observed without automatic rearming. Invalid or uncertain data remains reviewable. Imported elapsed deadlines can expire during the subsequent maintenance pass; migration itself never deletes or expires notes. Preserve note revisions during migration; older binaries refuse schema 3 rather than writing incompatible state.

### Maintenance and asynchronous reconciliation

Startup performs migration/verified backup before any maintenance. The TUI load worker expires all due notes and purges eligible Trash before listing. While open, one maintenance worker runs at most once per 30 elapsed seconds, independent of focused note/overlay; UI countdown updates can remain once per second. No overlapping maintenance workers. Explicit open/restore checks observe current deletion state. Existing CLI capture routes through the same deadline derivation on save; startup maintenance also runs for CLI invocations. Do not add CLI list/search subcommands in M2.

Maintenance runs transactions using supplied time, an indexed expiry cutoff and strict Trash cutoff. Expiry records `expired` and increments revision once; repeated/parallel scans do not overwrite deletion time or extend recovery. Purge is atomic with tags/FTS. Report immutable changed-ID/revision/time records, never dump content in logs. Invalid timestamps are not purge candidates.

Exclude the active note and retained dirty/failed/saving drafts from a bulk sweep. Flush/resolve due owned drafts through the existing save queue, then expire their acknowledged revision. A failed save postpones that local expiry and keeps an actionable draft. If editing/opening happens after a maintenance snapshot, or another process expires a note, preserve the newer local buffer/history and expose recovery; a successful stale write must still be reconciled, never simply ignored. Before deleting anything, compare persisted revision and lifecycle state in the same transaction. Cross-process unsaved memory cannot be locked by another TUI; recovery must retain local text when external expiry wins.

### Picker, pinning and Trash

Picker choices: 15 minutes, 1 hour, Today (23:59:59 local), 3 days, 7 days, Never, Custom. Fixed durations use elapsed time; Today uses the local date and location. Custom accepts a positive duration (`15m`, `2h`, `3d`) or RFC3339 with explicit offset. A local `YYYY-MM-DD HH:MM` input may also be accepted only if it resolves to one unique instant; reject nonexistent/ambiguous DST times with a request for an offset. Show the absolute local date/time and zone/offset before confirmation. Compute relative choices at confirmation, not when the menu first opened. Reject already-past deadlines.

Metadata requests resolve the latest draft save first and CAS the displayed revision; results update only their owner and preserve newer editor content. Pin clears expiry atomically. Unpin prompts to confirm continuing with no timer; it does not resurrect the cleared deadline. Manual deletion of pinned notes still works.

Trash is read-only. Restore offers Cancel, Restore without timer (default safe restore), and Restore and reset timer only for a validated remembered duration. A preset duration is not inferable from an absolute expired deadline; omit reset in that case and explain that expiration can be set after restore. Default restore clears expiry and records current directive fingerprint. Reset sets a new deadline from the injected current time. Undo last move-to-Trash restores the same ID and no timer. Permanent deletion defaults to Cancel and guards ID/revision/trashed state; no bulk purge UI is planned.

### Ghosts

New Ghost allocates a distinct memory-session identity before accepting any content. No ghost content is ever passed to repository save/CAS, tag/FTS updates, exports, clipboard tools or logs implicitly. Navigation retains it only in the current in-memory session; persisted search results never contain ghost content. Discard clears its draft/history/references; restart cannot recover it. Local `#kill` text can remain inert in a ghost; persisted TTL and pin actions show disabled explanations.

Ctrl+G remains accessible but opens safe conversion choices instead of silently deleting/persisting content. Duplicate conversion snapshots the latest local draft into a new ghost and preserves the source session; a dirty source follows ordinary save/conflict handling before navigation. The explicit Trash conversion must save/resolve the original, then CAS soft-delete it. Captured source key/version/revision prevents conversion of the wrong note. If edits occur during conversion, retain both the latest source draft and the captured ghost snapshot with an explanation. Existing originals must never disappear because an async completion was stale.

## Test/review contract

T-07/T-20/T-21/T-22/T-23/T-31 are mandatory M2 acceptance. T-05/T-06/T-08/T-13/T-17/T-18/T-19/T-25/T-27/T-28/T-35 remain regression gates. Use temporary synthetic fixtures, fake clock, independent SQLite connections/processes, and controlled out-of-order worker completions. Real database backups/migrations are never exercised by agent tests. Human platform checks remain explicitly NOT RUN unless actually performed.

The implementation plan assigns exact files, interfaces, failure cases, per-task red/green/full gates, migration recovery tests, PTY scripts and agent boundaries. Planning approval is not an execution result.
