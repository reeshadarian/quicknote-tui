# Agent Instructions — Quicknote v2

This repository is a Go + Bubble Tea + Lip Gloss + SQLite terminal scratchpad. Quicknote v2 is governed by:

1. `docs/QUICKNOTE_V2_SPEC.md` — **full, self-contained engineering specification** (scope, acceptance tests, dependencies, architectural decisions).
2. `docs/IMPLEMENTATION_PROGRESS.md` — **living execution state** (task statuses, commits, tests, blockers, handoffs).

Read both in full before editing. Inspect `git status`, `git log -5 --oneline`, and the current code; do not assume the documented October 2026 baseline is unchanged. Do not claim a test passed unless you ran it. Do not claim data was migrated or a backup created unless verified.

## Working protocol

- Select exactly one `QN-###` task that is READY (or the user's explicitly named ID). Prioritize data-loss P0 tasks before UI/feature polish.
- Run baseline tests before coding; add targeted regression tests; implement only the selected task; run applicable gates from spec §12.
- Use temporary synthetic SQLite fixtures; **never test destructive migrations or deletion against real notes**. Back up an existing database with SQLite's backup/VACUUM INTO mechanism before modifying schema or retention behavior.
- Preserve stable note IDs, per-note undo/dirty state, CAS revisions, async request generations and 72-hour trash recovery.
- Do not promise secure erasure; do not insert ghost notes into the database. Do not alter CLI stdout in quiet mode.
- Use keyboard-accessible paths for all new features. Preserve compatibility or document any intentional keymap change.
- After every task, update `docs/IMPLEMENTATION_PROGRESS.md` with branch/SHA, changed files, exact tests PASSED/FAILED/NOT RUN, risks, DB version, and next ready task. Commit it with the implementation.
- If requirements conflict with the working tree, report the discrepancy and propose a versioned spec amendment rather than improvising silent scope changes.

## Entry prompt for a resumed coding session

> Read `AGENTS.md`, `docs/QUICKNOTE_V2_SPEC.md`, and `docs/IMPLEMENTATION_PROGRESS.md`; inspect current git state, choose the next READY P0/P1 task, implement its acceptance criteria with tests, and update the progress file. Report exact commits, tests, and blockers. Do not implement unrelated features.

The original spec was produced by public static inspection, not by running tests or changing repository code. The first task is `QN-001` unless current progress records otherwise.