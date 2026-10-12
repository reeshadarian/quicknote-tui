# Quicknote v2 — Implementation Progress (Living Ledger)

**Specification:** `docs/QUICKNOTE_V2_SPEC.md` v1.0.0  
**Repository:** https://github.com/reeshadarian/quicknote-tui  
**Baseline inspected:** public `main`, 2026-10-11; SHA not yet pinned  
**Current HEAD:** UNVERIFIED (fill in from `git rev-parse HEAD`)  
**Current branch:** UNVERIFIED  
**Last updated:** 2026-10-11 (initial planning ledger)  
**Current milestone:** M0  
**Current task:** QN-001 — READY  
**Implementation completed so far:** None verified. This is a plan only.

> **Important:** The complete feature contracts, architectural decisions, acceptance scenarios T-01 through T-35, and cross-session protocol live in the spec. Do not treat this ledger as a substitute for reading it. This file records *execution*, not aspirations.

## First-run checklist

- [ ] Clone/open repo and pin exact baseline SHA in this file
- [ ] Check working-tree changes; do not overwrite existing work
- [ ] Check `go version`, `go.mod`, `go test ./...`, `go vet ./...`, and `go build ./...`
- [ ] Use only synthetic tests; never run destructive fixtures on user's real `notes.sqlite`
- [ ] Establish transactional SQLite backup method before migration
- [ ] Establish T-01/T-05/T-04 failing regression cases before making fixes
- [ ] Update QN-001 evidence and select first unblocked task

## Task status (all unimplemented at initial publication)

| Task | Milestone | Priority | Status | Commit SHA | Acceptance evidence |
|---|---|---|---|---|---|
| QN-001 — Establish baseline tests | M0 | P0 | READY | — | — |
| QN-002 — Fix search-to-editor identity | M0 | P0 | TODO | — | — |
| QN-003 — Isolate undo/save/navigation state | M0 | P0 | TODO | — | — |
| QN-004 — Unicode/UTF-8 safe editing | M0 | P0 | TODO | — | — |
| QN-005 — Preserve duplicate notes in trash | M0 | P0 | TODO | — | — |
| QN-006 — DB error propagation + transactions | M0 | P0 | TODO | — | — |
| QN-007 — Pure rendering and timer side effects | M0 | P0 | TODO | — | — |
| QN-008 — CLI quiet/error baseline | M0 | P0 | TODO | — | — |
| QN-009 — Extract typed Store API | M1 | P1 | TODO | — | — |
| QN-010 — Version-2 schema + backup migration | M1 | P1 | TODO | — | — |
| QN-011 — Compare-and-swap concurrency | M1 | P1 | TODO | — | — |
| QN-012 — Async commands with stale result protection | M1 | P1 | TODO | — | — |
| QN-013 — EditorDraft and authoritative NoteID | M1 | P1 | TODO | — | — |
| QN-014 — Central action registry | M1 | P1 | TODO | — | — |
| QN-015 — Versioned configurable keymap | M1 | P1 | TODO | — | — |
| QN-016 — Shared responsive layout engine | M1 | P1 | TODO | — | — |
| QN-017 — Overlay stack and confirmation system | M1 | P1 | TODO | — | — |
| QN-018 — Filter/query grammar | M3 | P1 | TODO | — | — |
| QN-019 — Safe indexed search backend | M3 | P1 | TODO | — | — |
| QN-020 — Bounded fuzzy ranking | M3 | P1 | TODO | — | — |
| QN-021 — Search results and snippets | M3 | P1 | TODO | — | — |
| QN-022 — Quick switcher | M3 | P1 | TODO | — | — |
| QN-023 — Command palette | M3 | P1 | TODO | — | — |
| QN-024 — Optional recent/tags sidebar | M3 | P1 | TODO | — | — |
| QN-025 — Tag filters and navigation | M3 | P1 | TODO | — | — |
| QN-026 — Searchable contextual help | M3 | P1 | TODO | — | — |
| QN-027 — Note navigation history | M3 | P2 | TODO | — | — |
| QN-028 — Durable TTL engine | M2 | P1 | TODO | — | — |
| QN-029 — TTL picker and status countdown | M2 | P1 | TODO | — | — |
| QN-030 — Pinning | M2 | P1 | TODO | — | — |
| QN-031 — Ghost-from-creation semantics | M2 | P1 | TODO | — | — |
| QN-032 — Trash UI and restore rules | M2 | P1 | TODO | — | — |
| QN-033 — Theme system and Catppuccin variants | M4 | P1 | TODO | — | — |
| QN-034 — Incremental syntax highlight/render | M4 | P1 | TODO | — | — |
| QN-035 — Mouse hitmap + clickable controls | M4 | P1 | TODO | — | — |
| QN-036 — Selection and clipboard | M4 | P1 | TODO | — | — |
| QN-037 — Restrained UI transitions | M4 | P2 | TODO | — | — |
| QN-038 — Status, notifications, error UX | M4 | P1 | TODO | — | — |
| QN-039 — Safe asynchronous shell pipeline | M5 | P1 | TODO | — | — |
| QN-040 — Shared CLI/storage behavior | M5 | P1 | TODO | — | — |
| QN-041 — Export/backup/restore | M5 | P1 | TODO | — | — |
| QN-042 — User config and settings UI | M4 | P2 | TODO | — | — |
| QN-043 — CI, lint, race, migration jobs | M6 | P1 | TODO | — | — |
| QN-044 — Benchmark suite and optimization | M6 | P1 | TODO | — | — |
| QN-045 — Platform compatibility matrix | M6 | P1 | TODO | — | — |
| QN-046 — Documentation and v2 release | M6 | P1 | TODO | — | — |
| QN-047 — Optional enhanced CLI commands | M5 | P2 | TODO | — | — |
| QN-048 — Preserve math/code/snippets | M1 | P1 | TODO | — | — |

## Active task card

- **Task:** QN-001 Establish baseline tests
- **Prerequisites:** None
- **Branch:** Not created
- **Target acceptance:** Baseline build/test report, temporary SQLite fixtures, initial reproducible regression tests. See spec §14 and §16.
- **Implementation notes:** No code changes yet.
- **Tests to run:** `go test ./...`, `go vet ./...`, `go build ./...`; race test when supported.
- **Risks:** Existing user notes must not be touched. Running git clone is outside this ledger's authority; implementer must inspect local working copy.

## Last completed session

- **Date:** 2026-10-11 (planning session only)
- **Scope performed:** Wrote spec and task ledger; inspected public source.
- **Files changed in repository:** None.
- **Commit SHA:** None.
- **Tests passed:** None claimed.
- **Tests failed:** None run.
- **Tests not run:** All code build, unit, race, integration, terminal, and migration tests — no local repository clone was available in the authoring environment.
- **Schema and backup:** Existing schema inferred from inspected source; no migration or backup attempted.
- **Open blockers:** Fetch current local SHA; establish fixtures and tests. No other blocker established.
- **Next:** QN-001.

## Decision log

| Date | Decision ID | Decision | Rationale | Spec change needed? |
|---|---|---|---|---|
| 2026-10-11 | DEC-001 | Keep Go + Bubble Tea + Lip Gloss + SQLite/FTS5. | Preserve performance and established shell interoperability. | Already specified. |
| 2026-10-11 | DEC-002 | Preserve deleted duplicates independently for 72h. | Data recoverability above dedup storage savings. | Already specified. |
| 2026-10-11 | DEC-003 | Default Ctrl+K = palette; legacy map can retain line-delete. | Discoverability and transparent shortcut migration. | Already specified. |
| 2026-10-11 | DEC-004 | No secure-erasure marketing claim. | SQLite / swap / backups prevent such a guarantee. | Already specified. |

## Open bugs, risks, and discrepancies

| Reference | Severity | Status | Next action |
|---|---|---|---|
| BUG-01 (search opens wrong identity) | P0 | Static-source finding, untested | QN-002 after T-01/T-02 reproducible |
| BUG-02 (duplicate content hard-delete) | P0 | Static-source finding, untested | QN-005 after T-05 fixture |
| BUG-03 (global undo) | P0 | Static-source finding, untested | QN-003 with T-03 |
| BUG-04 (byte/rune splice) | P0 | Static-source finding, untested | QN-004 with T-04 |
| New migration / existing backups | P1 | Not begun | QN-009/010 with verified backup |
| Public main may have moved since inspection | P1 | Unknown | Recheck current git SHA and revalidate code |

## Verification and release gates

- [ ] P0 data-integrity and retention regressions pass
- [ ] SQLite schema migration from legacy fixtures and backup/restore verified
- [ ] Concurrent CAS saves / stale async completions verified
- [ ] Persistent TTL, ghost creation, pin, and Trash policies pass
- [ ] Search grammar, fuzzy ranking, quick switcher, palette and keyboard help verified
- [ ] Responsive layout, theme fallback, mouse, and keyboard-only path verified
- [ ] CLI quiet output, shell timeout/cancel, and export error handling verified
- [ ] Benchmarks run with hardware/fixture configuration recorded
- [ ] CI enabled and passing; no misleading test claims
- [ ] README installation/version/privacy claims updated; release notes written

## Append-only session reports

Use this template for each completed coding session:

```text
SESSION <YYYY-MM-DD hh:mm zone>
Task: QN-###
Branch/HEAD before: ...
Branch/HEAD after: ...
Files changed: ...
Scope implemented: ...
Design decisions: ...
Tests PASSED: [exact commands]
Tests FAILED: [exact commands/reasons]
Tests NOT RUN: [commands/reasons]
Database migration + backup: ...
Open bugs/blockers: ...
Next READY task: ...
```

**End of initial ledger.**