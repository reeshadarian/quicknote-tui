# Quicknote v2 — New Session Handoff

**Repository:** https://github.com/reeshadarian/quicknote-tui
**Working branch:** `dev/v2`
**Protected branch:** `main` (do not change or merge without explicit approval)
**Last maintainer-verified baseline:** 2026-10-11
**Latest development verification:** QN-011, 2026-10-12 UTC; full suite/race/vet/build and scripted PTY/CLI checks passed. See the living ledger for exact commits and evidence.

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
- QN-011 is implemented and verified: content/tag/metadata writes use revision CAS; conflicting TUI drafts retain base revisions and offer keyboard copy/compare/confirmed reload. Separate connections and separate processes pass T-17. Schema stays v2. Durable expiration/pinning UX remains future work.
- No new Go test runs are implied by uploading these documents. Always distinguish *previously reported pass* from *tests actually run in this session*.

## Next task: QN-012 — async commands with stale result protection

Read the full specification, especially §§3.2–3.4 and §14. QN-011's synchronous CAS is now the storage baseline; preserve it while moving I/O off the event loop.

- Introduce typed save/load/search/highlight messages and monotonically increasing request generations.
- Capture immutable note ID, base revision and local-version snapshots for each job. Apply results only to the matching note/request; a save completing after another edit may update its base revision but must keep that newer draft dirty.
- Keep the conflict recovery paths and original unsaved buffers. Never refresh a revision and overwrite automatically.
- **T-02 and T-24 must pass**, alongside QN-011/T-17 and the existing suite.
- Inspect `store.go`, `tui_conflict.go`, `tui.go`, and the new `cas_*test.go` regressions. Production DB opens use `_busy_timeout=5000&_txlock=immediate`.
- Leave the broader index-to-Draft conversion for QN-013 and unrelated lifecycle/UI work for their assigned tasks.

Conflict keyboard flow: `C` copy, `I` compare, arrows/PageUp/PageDown scroll, `R` twice reload/discard confirmation, `Esc` keep editing; Enter from the persistent error status retries/reopens recovery. A local conflicting draft can be saved as a separate note even when its remote note was trashed/purged. Saving remains synchronous until this next task.

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

> Continue development of https://github.com/reeshadarian/quicknote-tui on `dev/v2`. The repository now contains all self-contained project documents; do not ask for an upload or rely on prior chat context. Read `AGENTS.md`, the entire `docs/QUICKNOTE_V2_SPEC.md`, `docs/IMPLEMENTATION_PROGRESS.md`, and `docs/SESSION_HANDOFF.md`. Inspect the current branch and recent commits, then implement **QN-012 typed asynchronous commands with stale-result protection** as specified, including T-02 and T-24. Preserve the verified QN-011 CAS and separate-process/connection T-17 regressions. Preserve unsaved drafts and CLI compatibility, commit changes to `dev/v2`, update the progress ledger, and report tested versus untested outcomes. Never modify `main` without my approval.
