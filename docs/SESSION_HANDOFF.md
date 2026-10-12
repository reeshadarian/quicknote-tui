# Quicknote v2 — New Session Handoff

**Repository:** https://github.com/reeshadarian/quicknote-tui
**Working branch:** `dev/v2`
**Protected branch:** `main` (do not change or merge without explicit approval)
**Last maintainer-verified baseline:** 2026-10-11

## Start here: authoritative documents (all in this repository)

1. [`AGENTS.md`](../AGENTS.md) — coding-agent instructions and safety rules.
2. [`docs/QUICKNOTE_V2_SPEC.md`](QUICKNOTE_V2_SPEC.md) — the **complete 987-line specification**, task dependencies, architecture, T-01–T-35 acceptance scenarios, milestone definition, and design decisions.
3. [`docs/IMPLEMENTATION_PROGRESS.md`](IMPLEMENTATION_PROGRESS.md) — the **living implementation ledger** with previous commit SHAs, remaining risks, and verification evidence. This supersedes the original planning template.
4. [`docs/LOCAL_TESTING.md`](LOCAL_TESTING.md) — required Fedora/Go/SQLite FTS5 commands and isolated HOME testing.
5. [`docs/QN-001-BASELINE.md`](QN-001-BASELINE.md) — original regression baseline and known historical bugs.
6. [`docs/IMPLEMENTATION_PROGRESS_TEMPLATE.md`](IMPLEMENTATION_PROGRESS_TEMPLATE.md) — **historical initial 48-task ledger only**. Do not reset or replace the living progress file with it.

**Important:** You do not need the previous ChatGPT conversation or ChatGPT Library: all the source-of-truth Markdown documents are checked into `dev/v2`. If any file is unavailable, inspect the live GitHub branch before drawing conclusions.

## Verified current development state

- QN-001 through QN-010 have been **implemented and committed** on `dev/v2`; their current regression tests passed locally after follow-up fixes. This does not establish that every legacy path has been migrated or every feature is complete.
- Maintainer ran `go test -count=1 -v ./...` and `go test -race -count=1 ./...` with SQLite FTS5 enabled: **PASS** (2026-10-11; race test returned `ok quicknote 1.080s`).
- Maintainer additionally confirmed manual note creation, autosave, search result identity, undo isolation, Trash and duplicate restoration, Unicode editing, wrap/resize, note switching, quiet capture, title/recent append, timed expiration, pipeline capture, and the V1 → V2 migration regression test.
- One manual test uncovered **missing permanent deletion from Trash UI**. `SQLiteStore.Purge` exists, but Trash lacks a discoverable confirmation workflow; plan it in the appropriate UI/action milestone.
- Trailing newline behavior during editing may require a targeted follow-up investigation.
- Some transitional `NoteStore` adapters still suppress errors; address this during the appropriate refactor, not silently as unrelated work.
- The V2 schema has `revision`, `expires_at`, and `is_pinned`, but CAS write protection and durable expiration/pinning workflows are **not implemented yet**.
- No new Go test runs are implied by uploading these documents. Always distinguish *previously reported pass* from *tests actually run in this session*.

## Next task: QN-011 — optimistic compare-and-swap revisions

Use §14 of `docs/QUICKNOTE_V2_SPEC.md` (and related contracts) for the exact acceptance criteria; **T-17 must pass**.

- Inspect `Note`, `SQLiteStore`, `saveNoteChecked`, `main.go` CLI append, the TUI save/dirty-draft paths, and database revision migration.
- Load the authoritative revision with each persisted note.
- Update using a compare-and-swap SQL predicate on `id` **and expected revision**. Increment revision atomically only when one row matches.
- Return a typed conflict error for stale revisions; distinguish not-found/trashed records and genuine SQLite errors.
- Preserve unsaved local content after a conflict. Do not automatically merge, drop, or silently overwrite notes.
- Make CLI append/update respect revision semantics while preserving the existing quiet-mode contract.
- Add regression tests using **two separate SQLite connections** and verify that the second stale writer cannot overwrite the first writer. Include metadata updates and edge cases appropriate to T-17.
- Do not enable schema fields as user-facing features prematurely.

## Fedora / Zsh verification commands

```zsh
git fetch origin
git switch dev/v2
git pull --ff-only origin dev/v2
git status --short
go version
export CGO_ENABLED=1
export GOFLAGS="-tags=sqlite_fts5"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/quicknote-test .
```

For manual testing (do not touch real notes):

```zsh
export QN_TEST_HOME="$(mktemp -d)"
qntest() { HOME="$QN_TEST_HOME" /tmp/quicknote-test "$@"; }
printf 'one\ntwo\n' | qntest -q
qntest
```

The application normally stores real user notes in `~/.local/share/quicknote-tui/notes.sqlite`. **Never run destructive test/migration operations against that file.** Existing FTS5 requires `sqlite_fts5`; without it SQLite will fail with `no such module: fts5`.

## Workflow for each coding session

1. Read all three governing files: `AGENTS.md`, `docs/QUICKNOTE_V2_SPEC.md`, and `docs/IMPLEMENTATION_PROGRESS.md`.
2. Inspect actual current branch HEAD and source. The working tree always takes precedence over historical descriptions.
3. Implement the named ready task, preferably adding regression tests before changing code.
4. Run available verification; report any tool/runtime limitation without falsely claiming tests passed.
5. Commit only to `dev/v2`; update the living ledger with task state, commit SHA, changed files, test evidence, blockers, and next task.
6. Do not overwrite historical evidence or merge to `main` without user approval.

## Ready-to-paste prompt for the next ChatGPT session

> Continue development of https://github.com/reeshadarian/quicknote-tui on `dev/v2`. The repository now contains all self-contained project documents; do not ask for an upload or rely on prior chat context. Read `AGENTS.md`, the entire `docs/QUICKNOTE_V2_SPEC.md`, `docs/IMPLEMENTATION_PROGRESS.md`, and `docs/SESSION_HANDOFF.md`. Inspect the current branch and recent commits, then implement **QN-011 optimistic revision-based compare-and-swap concurrency** as specified, including T-17 and two-connection regression tests. Preserve unsaved drafts and CLI compatibility, commit changes to `dev/v2`, update the progress ledger, and report tested versus untested outcomes. Never modify `main` without my approval.
