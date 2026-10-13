# Agent Instructions — Quicknote v2

<!-- gitbutler-agent-setup:start -->
## Version control

- Use GitButler (`but`) for version-control inspection and write operations, including status, diffs, branching, committing, pushing, and history edits.
- Assume multiple agents may be working in this repository. Do not move, amend, squash, discard, commit, push, or otherwise modify another agent's work unless the user asks.
- For commit just/only/specific changes on a new branch (selected-change requests), use the two-command fast path from the GitButler skill: `but diff`, then `but commit -b <branch> -m "message" <id> <id>`.
- For that fast path, after the commit succeeds, stop and summarize; do not run separate branch, staging, status, or diff commands unless the commit output is missing information you need.
- Use the installed GitButler skill for command recipes and syntax before guessing flags, using `--help`, or translating Git habits directly.
- Mutation commands report their result without appending workspace status. Add `--status-after` only when the next step needs resulting workspace IDs or details; otherwise do not rerun status or diff to verify success.
- Use a dedicated GitButler branch for each agent session, unless the user asks for a different branch structure. Commit only changes that belong to that session.
- Do not push or open pull requests unless the user asks.
- Keep commit messages and pull request descriptions succinct: explain what changed, why it changed, and any important decision.

### Amend local fixes into the right commits

- For small cleanup or follow-up fixes, amend an unpublished local commit when the change clearly belongs with that commit's intent.
- Do not create tiny fixup commits unless the user asks.
- Use GitButler to move the relevant changes into the commit where they belong.
- Ask before rewriting pushed, reviewed, shared, or ambiguous history.

### Split unrelated changes into separate commits

- If one file contains unrelated changes, split them by hunk instead of committing the whole file.
- Keep tests with the behavior they verify.
- Split generated output, docs-only edits, or mechanical cleanup into separate commits when each commit remains coherent on its own.
- If the split is ambiguous, summarize the options before committing.
<!-- gitbutler-agent-setup:end -->

--- project-doc ---

This repository is a Go + Bubble Tea + Lip Gloss + SQLite terminal scratchpad. Quicknote v2 is governed by:

1. `docs/QUICKNOTE_V2_SPEC.md` — **full, self-contained engineering specification** (scope, acceptance tests, dependencies, architectural decisions).
2. `docs/IMPLEMENTATION_PROGRESS.md` — **living execution state** (task statuses, commits, tests, blockers, handoffs).
3. `docs/SESSION_HANDOFF.md` — **self-contained continuation instructions** and current next task.
4. `docs/LOCAL_TESTING.md` — Fedora/Zsh setup, SQLite FTS5 build flags, safe test commands.

The original initial ledger is archived at `docs/IMPLEMENTATION_PROGRESS_TEMPLATE.md`. Never replace the living ledger with that template.

Read both in full before editing. Inspect the current GitButler branch/commit state (`but status`, `but show`) and the current code; do not assume the documented October 2026 baseline is unchanged. Do not claim a test passed unless you ran it. Do not claim data was migrated or a backup created unless verified.

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

> Read `AGENTS.md`, `docs/QUICKNOTE_V2_SPEC.md`, `docs/IMPLEMENTATION_PROGRESS.md`, and `docs/SESSION_HANDOFF.md`; inspect current git state, continue the approved M2 execution one task at a time (final M2 gates/review, then QN-018 unless the branch has advanced), implement its acceptance criteria with tests, and update the progress file. Report exact commits, tests, and blockers. Do not implement unrelated features.

The original specification was produced from public static inspection; implementation and verified regression tests followed on `dev/v2`. QN-001–QN-010 have been committed and the maintainer reported passing Go/race tests on 2026-10-11. QN-011 CAS and conflict recovery were implemented and passed Go/race/vet/build, independent-process/connection concurrency tests, and scripted PTY/CLI checks on 2026-10-12 UTC. QN-012 typed asynchronous save/load/search/highlight commands and stale-result protection are implemented and passed Go/race/vet/build, T-02/T-24/T-17 repetitions, and scripted PTY/CLI checks on 2026-10-12 UTC. QN-013–QN-017 have been implemented and verified on dev/v2, with separate task commits and full Go/race/vet/build, Unicode fuzz, repeated acceptance and scripted PTY/CLI checks. QN-048 and QN-028–032 task implementations are now accepted and verified; the user approved execution of all M2 tasks. Finish aggregate M2 gates and independent milestone review before QN-018. Inspect the living ledger and actual GitButler branch before selecting work. Publish only to dev/v2 under the existing explicit M2 authorization; never alter or merge main.
