# Quicknote v2 — Self-Contained Engineering Specification

**Spec ID:** QN-V2  
**Version:** 1.0.0  
**Prepared:** 2026-10-11  
**Project:** [reeshadarian/quicknote-tui](https://github.com/reeshadarian/quicknote-tui)  
**Reference branch:** `main`, as publicly inspected 2026-10-11; **commit SHA was not verified**.  
**Implementation state at authorship:** Planning only. No changes to the repository have been made by this specification.  
**Status:** Proposed, implementation-ready specification; the maintainer remains the final authority on scope and changes.

> **North star:** Capture instantly. Find effortlessly. Manipulate freely. Forget predictably.
>
> Quicknote is an *ephemeral terminal scratchpad*, not a permanent knowledge-management platform. Preserve its speed, plain-text interoperability, and keyboard orientation while making it safer, more discoverable, mouse-friendly, visually polished, and easy to maintain.

---

## 0. How to use this specification across independent coding sessions

This is the **single source of truth for scope, contracts, architecture, milestones, task definitions, and acceptance tests**. It is intentionally independent of the current chat. Keep this file versioned in the repository at `docs/QUICKNOTE_V2_SPEC.md`. Keep the companion `docs/IMPLEMENTATION_PROGRESS.md` updated between sessions. If the companion is missing, recreate it from §15 and the task ledger in §14.

### Start-of-session procedure (mandatory)

1. Read **this entire spec** and the current `docs/IMPLEMENTATION_PROGRESS.md` before changing code. Read `git status`, `git log -5 --oneline`, and `go.mod`.
2. Check whether the repository's current code diverges from the inspected baseline (§2). Treat the **actual checked-out code** as authoritative for implementation details; do not pretend this spec is a code snapshot. Record new deviations in the progress document.
3. Select **one task ID** in §14 whose prerequisites are satisfied. Prefer highest priority and earliest dependency order. If already in progress, finish it before starting another.
4. Create a focused branch named `qn-v2/<TASK-ID>-short-name` (or use a similarly traceable branch in an existing workflow). Protect user data: back up the SQLite DB before schema, deletion, or retention changes. Never commit real notes or database copies.
5. Add or update the regression test **before the fix** where practical. Implement the smallest change completing that task. Do not simultaneously rewrite unrelated subsystems.
6. Run task-specific tests and the baseline gates (§12). Perform manual terminal tests where required; if the environment lacks a PTY or terminal, mark these tests **not run**, not passed.
7. Update the ledger and session report (§15) with exact changed files, commit hash, tests run/results, migration state, unresolved risks, and the next ready task. No task is `DONE` until its acceptance criteria are verified.

### Ready-to-paste prompt for any new ChatGPT or coding-agent session

```text
You are implementing Quicknote v2 in https://github.com/reeshadarian/quicknote-tui.
Read docs/QUICKNOTE_V2_SPEC.md and docs/IMPLEMENTATION_PROGRESS.md in full.
Inspect the current repository and git status; do not assume either file describes the latest code.
Choose the earliest unblocked P0/P1 task (or the explicitly assigned task ID),
state its scope and acceptance tests, then implement only that task.
Preserve all existing notes and CLI behavior unless the spec explicitly defines a migration.
Write regression tests, run all available gates, and report any tests you could not run.
Update IMPLEMENTATION_PROGRESS.md with status, commit hash, files changed,
verification, risks, and the next recommended task.
Do not claim an unrun test passed; do not silently change the scope.
```

### Status vocabulary and evidence rules

- `TODO`: not started. `READY`: all prerequisites done. `IN_PROGRESS`: currently being implemented. `BLOCKED`: document the precise blocker. `REVIEW`: code complete, acceptance pending. `DONE`: acceptance tests passed and changes committed. `DEFERRED`: consciously excluded from the release with maintainer approval.
- A task is never done solely because code compiles, a mockup exists, or an AI says it is done.
- Record the git SHA of each completed task and preserve identifiers forever. If plans change, amend this document and increment its version; do not silently reinterpret an existing task.
- When a task needs external research, verify library APIs against the version actually used in the repo. No unverified dependency upgrades just to fulfill UI polish.

---

## 1. Product contract, goals, and exclusions

### 1.1 Primary use cases

1. Open Quicknote and start typing a titleless scratch note immediately.
2. Capture shell output (`printf ... | quicknote -q`) without unexpected stdout or formatting changes.
3. Find a recent note with a few characters, correct typos, and jump to an exact matching location.
4. Browse recent notes, inspect metadata, pin important temporary notes, and return to editing using either keyboard or mouse.
5. Set a note to expire in 15 minutes, one hour, at end of day, a few days, or never; reliably enforce that decision across restarts.
6. Recover an accidentally deleted or expired persisted note within a three-day trash window.
7. Create a **new** memory-only ghost note that is never deliberately persisted; understand the limitations of this privacy mode.
8. Run a deliberately invoked shell filter (`jq`, `sort`, etc.) over note content with review/undo and failure handling.
9. Use markdown/code formatting, simple calculations, date snippets, and clipboard/export without losing text.
10. Navigate visually polished layouts using keyboard only, with optional mouse affordances and responsive, font-agnostic fallbacks.

### 1.2 Goals and measured outcomes

- **G1 Data integrity:** zero cross-note writes, zero content loss on failed save, no accidental permanent deletion of persisted content during the retention period.
- **G2 Discoverability:** every user-visible action searchable in one command palette; prominent shortcut hints and contextual help.
- **G3 Efficiency:** start typing without navigating an onboarding dialog; mouse is optional, never required; normal keypresses never block behind SQLite or a shell pipeline.
- **G4 Design:** coherent theme tokens, readable hierarchy, restrained motion, responsive layouts, keyboard focus and input feedback.
- **G5 Lifecycle:** one explicit note-state model for scratch/pinned/trashed/ghost; expiration persists and applies even if the TUI is closed.
- **G6 Operability:** scripted install/build/test/release, reproducible benchmarks, and complete handoff records across sessions.

### 1.3 Explicit non-goals for v2

- Cloud synchronization, accounts, sharing, mobile applications, collaboration, network services.
- Rich-text/WYSIWYG editing, web/Electron UI, LSP/IDE complexity, plugin marketplace, encryption claims, secure erasure guarantees.
- Unrestricted scripting or remote shell execution beyond explicitly user-invoked **local** shell pipelines.
- A new terminal framework or a new database engine: continue with Go, Bubble Tea, Lip Gloss, SQLite/FTS5.
- A guarantee of *sub-millisecond* startup or any performance metric not verified by reproducible benchmarks.

### 1.4 Non-negotiable invariants (apply to every task)

| ID | Invariant |
|---|---|
| INV-01 | Any persisted note has a stable `NoteID`; list positions, search result positions, and UI tab indices are never authoritative identities. |
| INV-02 | An asynchronous result is applied only to the correct `NoteID` and expected revision/request generation. Stale saves/highlights/search results cannot overwrite newer changes. |
| INV-03 | Changing notes cannot replace the current draft with another note without first preserving it or reporting a recoverable save conflict. |
| INV-04 | Undo and redo cannot cross notes. Search, navigation, or trash browsing never reuses an unrelated note's history. |
| INV-05 | Normal delete and TTL expiry move persisted notes into recoverable trash for 72 hours. **Exact duplicates are still independent notes** and may not be permanently removed by deduplication. |
| INV-06 | Destructive operations and shell-output replacement are reversible where promised; irreversibility requires an explicit, clearly labeled action and confirmation. |
| INV-07 | A failed save remains visibly dirty and retryable; no false “Saved” indicator. |
| INV-08 | `View` and other rendering methods must be pure: no writes, timers reset, disk access, or mutation of application state. |
| INV-09 | Cursor positions and visual hit targets are grapheme/cell-width aware; never index UTF-8 strings with a rune offset. |
| INV-10 | Input, content, and search terms are treated as data, not executable shell commands, unless the user intentionally invokes the shell pipeline action. |
| INV-11 | A new ghost note never deliberately touches SQLite, clipboard, logs, crash dumps, backups, or search index. Terminal scrollback, OS memory/swap, and external clipboard tools cannot be guaranteed private. |
| INV-12 | CLI with `-q` emits no success messages to stdout; failures use stderr and nonzero exit codes. Never silently swallow errors. |
| INV-13 | All existing active/trash notes are preserved by automatic migrations; schema version upgrades are transactional, versioned, and testable from an old fixture. |
| INV-14 | Every feature works without a mouse, a Nerd Font, true color, or animations. Narrow and tiny terminals cannot cause panics. |

---

## 2. Verified baseline: code and current risks

### 2.1 Public source map (inspected 2026-10-11)

| File | Current responsibility | Important observations |
|---|---|---|
| [`main.go`](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/main.go) | CLI (`-n`, `-q`, `-t`, `-l`, `-c`), stdin, shell capture, Bubble Tea launch | `-q` prints a success message; some I/O errors ignored; CLI can directly update SQLite, bypassing shared save rules. |
| [`db.go`](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/db.go) | `Note`, `initDB`, `saveNote`, `searchNotes`, tags, trash, restore | `PRAGMA user_version` initial migration; FTS5 table and triggers; nontransactional save/tags; duplicate trash entries can be permanently deleted. |
| [`tui.go`](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/tui.go) | TUI `model`, `Update`, `View`, navigation, search, autosave, undo, mouse, shell piping | Search selection changes the editor content without establishing an active note ID; `Ctrl+K` currently deletes a line; mixed synchronous DB/shell work and UI state. |
| [`engine.go`](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/engine.go) | `[][]rune` conversions, wrap/layout, ANSI, highlight, math, `#kill` parser, theme | Unicode byte/rune/cell accounting needs tests; theme JSON only partially validated; tabs are converted to spaces on load. |
| [`utils.go`](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/utils.go) | Clipboard, title extraction, relative timestamps, tags, UUID | Clipboard errors ignored; clipboard fallback needs tmux/OSC 52 and status feedback. |
| [`go.mod`](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/go.mod) | Build dependencies | Declares `go 1.25.9`; README says Go 1.20+, so minimum version needs reconciliation. |
| [`README.md`](https://github.com/reeshadarian/quicknote-tui/blob/main/README.md) | User documentation | Describes FTS as “fuzzy” and async and deletes as “secure”; wording must match implementation and threat model. |

Current flags and notable shortcuts to preserve where practical: `-n`, `-q`, `-t`, `-l`, `-c`; `Ctrl+N` new, `Ctrl+F` search, `Ctrl+T` trash, `Ctrl+R` restore while in trash, `Ctrl+G` toggle ghost, `Ctrl+S` export, `Ctrl+Z/Y` undo/redo, `Alt+X` shell pipe, `Alt+C` copy, `Alt+←/→` notes, `F1` help, `F2` line numbers, `Alt+M/V` math/code mode, `Ctrl+K` **currently delete current line**. See §7 for intentional keymap migration.

### 2.2 Issue register and required verification

| Issue ID | Priority | What the code currently does | Required remedy | Regression ID |
|---|---|---|---|---|
| BUG-01 | P0 | Search Enter sets `m.buffer` to the selected result but not the current `m.index`/ID; subsequent save can target a different note. | Central `openNote(noteID)` transition and identity-based save. | T-01, T-02 |
| BUG-02 | P0 | `trashNote` hard-deletes a note if any other note has identical content. Startup also deletes duplicate trash rows. | Treat duplicate content as independent note records; 72h trash recovery. | T-05 |
| BUG-03 | P0 | Undo/redo stacks are stored globally on `model`, not bound to note identity. | Per-note histories or reset/restore them on open. | T-03 |
| BUG-04 | P0 | Backtick editing uses `line[:m.cursorCol]` even though `cursorCol` refers to runes. | Grapheme-safe edit primitives; no byte slicing from rune/cell indices. | T-04 |
| BUG-05 | P1 | Note and tag writes ignore several errors and aren't committed atomically. | `error`-returning repository operations and transactions. | T-06 |
| BUG-06 | P1 | Timers depend on active note's buffer, `KillHash`/`KillStart`, and mutate during rendering. | Persist `expires_at` UTC; expiry scan across all notes; pure renderer. | T-07, T-08 |
| BUG-07 | P1 | Search sends raw-ish user query into FTS5 with `*`, and treats SQL search failures as no results; it's not typo fuzzy. | Parsed, escaped FTS queries, bounded fuzzy title/tag fallback, visible errors. | T-09, T-10 |
| BUG-08 | P1 | Shell pipeline is synchronous in `Update` and may block; replacing content is immediate. | Async, timeout/cancel, preview/confirm, undo, stderr diagnostics. | T-11 |
| BUG-09 | P1 | CLI and UI save through different paths, and `-q` prints to stdout. | Shared service, consistent exit/error semantics. | T-12 |
| BUG-10 | P1 | `View` changes elements inside `m.notes` when updating the kill timer. | Pure `View`; all mutation through `Update`/messages. | T-13 |
| BUG-11 | P2 | Theme configuration can silently accept missing/invalid tokens; terminal widths require stronger fallbacks. | Config validation/default merging, 16/256/true-color options. | T-14 |
| BUG-12 | P2 | CLI retains/trims shell/stdin payload inconsistently, potentially losing meaningful whitespace. | Define exact input normalization and preserve bytes except documented transforms. | T-15 |
| BUG-13 | P2 | `Ctrl+K` already means delete line, conflicting with the proposed palette shortcut. | Versioned keymap migration and visible alternative. | T-16 |

All findings above are based on static source inspection. They are strong candidates for regression tests, **not claims that the test suite has already reproduced them**. Revalidate against the current working tree before fixing them.

### 2.3 Baseline capture before first change

Run locally and record the outputs in `IMPLEMENTATION_PROGRESS.md`:

```bash
git status --short
git rev-parse HEAD
go version
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...  # if race toolchain is available
go build -o /tmp/quicknote-baseline .
```

For DB-affecting work, snapshot **using SQLite's backup API or `VACUUM INTO` while no transaction is active**, rather than blindly copying a live WAL-mode `.sqlite` file. For an offline/manual backup, stop all writers and preserve main DB plus WAL/SHM together; never rely on a lone live database-file copy.

---

## 3. Target architecture and internal contracts

### 3.1 Incremental structure (not a big-bang rewrite)

```text
quicknote-tui/
├── cmd/quicknote/main.go            # CLI entry point, thin adapter
├── internal/app/
│   ├── model.go                      # stable active ID, draft state, overlays
│   ├── update.go                     # Bubble Tea message routing
│   ├── actions.go                    # action registry, contexts, dispatch
│   ├── keymap.go                     # configurable shortcuts
│   ├── messages.go                   # typed async operation results
│   └── commands.go                   # command factories / cancellation
├── internal/editor/
│   ├── buffer.go                     # text operations / invariants
│   ├── cursor.go                     # grapheme-aware cursor and selection
│   ├── layout.go                     # wraps: graphemes -> terminal cells
│   ├── history.go                    # isolated undo/redo transactions
│   └── highlight.go                  # highlighting with revisions
├── internal/store/
│   ├── store.go                      # interface, typed errors, Note
│   └── sqlite/
│       ├── connection.go             # pragmas, connection ownership
│       ├── migrations.go             # transactional schema versions
│       ├── notes.go                  # CRUD, revision compare-and-swap
│       ├── search.go                 # FTS5 and filtering
│       └── maintenance.go            # trash cleanup, index repair
├── internal/lifecycle/
│   ├── expiration.go                 # one source of truth for TTL
│   └── parser.go                     # #kill directive compatibility
├── internal/search/
│   ├── query.go                      # text and filter grammar
│   ├── ranking.go                    # fuzzy scoring, stable ties
│   └── snippet.go                    # grapheme-safe snippets / highlighting
├── internal/ui/
│   ├── layout.go                     # responsive layout decisions
│   ├── editor.go                     # pure rendering
│   ├── sidebar.go                    # recent/tags/trash/pinned
│   ├── palette.go                    # command picker
│   ├── switcher.go                   # note picker
│   ├── dialogs.go                    # confirmation, errors, preview
│   ├── hitmap.go                     # rect-based mouse event targets
│   ├── status.go                     # save / TTL / position state
│   └── themes.go                     # token palettes and fallbacks
├── internal/shell/
│   ├── pipeline.go                   # bounded local process execution
│   ├── clipboard.go                  # wl-copy/xclip/OSC52 alternatives
│   └── export.go                     # safe file writes
├── internal/config/config.go        # versioned user options/defaults
├── docs/
│   ├── QUICKNOTE_V2_SPEC.md
│   └── IMPLEMENTATION_PROGRESS.md
├── tests/                             # external fixtures + PTY/integration
└── .github/workflows/               # build/test/release
```

**Transition strategy:** Begin with adapter packages around the current top-level `main` package; move one responsibility at a time. Don't split files solely for aesthetics. Each extraction must preserve behavior and pass baseline tests. Do not introduce an abstraction unless at least two call sites or a test seam need it. Keep existing commands working throughout.

### 3.2 Core model contracts (illustrative Go, not drop-in code)

```go
type NoteID int64
type Revision int64

type Note struct {
    ID NoteID
    Content string
    Revision Revision
    CreatedAt time.Time // UTC in persistence
    UpdatedAt time.Time // UTC in persistence
    DeletedAt *time.Time
    ExpiresAt *time.Time
    IsPinned bool
    Tags []string
}

type Draft struct {
    NoteID NoteID       // 0 is unsaved persisted draft; never a list index
    LocalVersion uint64 // increments on every edit
    BaseRevision Revision
    Buffer EditorBuffer
    Dirty bool
    SavingVersion uint64
    Undo UndoHistory
    Viewport ViewportState
    Mode DraftMode      // persisted | ghost
}

type SaveRequest struct {
    NoteID NoteID
    BaseRevision Revision
    LocalVersion uint64
    Content string     // immutable snapshot, not a pointer to model buffer
}

type SaveResult struct {
    NoteID NoteID
    RequestVersion uint64
    NewRevision Revision
    Err error
}
```

**Required rule:** a save result may update the displayed save status or base revision **only** if it corresponds to the same note and request. If the user has edited since the request, keep dirty state true and schedule another save; never replace the newer draft buffer with an older response. If a concurrent process has written the note (`revision` conflict), preserve local text, show a conflict indicator and provide **Reload / Save as copy / Inspect difference**; never last-write-wins silently.

### 3.3 Repository API contract

```go
type Store interface {
    Create(ctx context.Context, n Note) (Note, error)
    Get(ctx context.Context, id NoteID) (Note, error)
    UpdateCAS(ctx context.Context, n Note, expected Revision) (Note, error)
    List(ctx context.Context, filter NoteFilter) ([]NoteSummary, error)
    Search(ctx context.Context, q SearchQuery, limit int) ([]SearchHit, error)
    MoveToTrash(ctx context.Context, id NoteID, reason TrashReason) error
    Restore(ctx context.Context, id NoteID) (Note, error)
    DeleteForever(ctx context.Context, id NoteID) error
    SetExpiration(ctx context.Context, id NoteID, at *time.Time) error
    SetPinned(ctx context.Context, id NoteID, pinned bool) error
    ExpireDue(ctx context.Context, now time.Time) (int, error)
    PurgeTrashOlderThan(ctx context.Context, threshold time.Time) (int, error)
    Close() error
}
```

Return typed `ErrNotFound`, `ErrConflict`, `ErrBusy`, `ErrReadonly`, and migration errors; never use an empty note to represent errors or an empty trash. Keep every write and derived tag/index update in a transaction; FTS triggers run with the related SQLite statement, not “asynchronously”. Foreign keys enabled and verified (`PRAGMA foreign_keys=ON`).

### 3.4 State transitions and async jobs

```text
                      New persisted draft
                              |
                              v
                       ACTIVE SCRATCH <---- restore ---- TRASH
                          |    |                           ^
                 pin/unpin|    |delete/TTL                 |purge after 72h
                          v    +--------------------------+-----> GONE
                        PINNED

  NEW GHOST (memory only) ---- close/discard ----> GONE
```

- `ActiveNoteID` is separate from result list index, visual list order, and `SearchHit` location. `openNote(id)` is the **only** path for switching editor identity, including search, sidebar, next/previous, trash restore, and quick switcher.
- `openNote` transitions: resolve current draft save or conflict; load target by ID; install target draft/history/cursor; update location separately; increment `openGeneration` to ignore stale loads.
- `Update` receives typed messages. SQL search, save, layout work beyond a frame budget, and local shell filters run in asynchronous Tea commands. Results are tagged with relevant `NoteID`, `LocalVersion`, and/or monotonically increasing `requestID`.
- Search uses a 75–125 ms debounce (configurable), cancellation if supported, and discards out-of-order results. Shell pipeline runs separately and cannot freeze keys, resize, or mouse events.
- `View` consumes immutable render state and returns a string. It must be deterministic given the same model and terminal capability snapshot. No DB calls or note mutation.

---
## 4. Storage, migration, expiration, and privacy specifications

### 4.1 Existing DB compatibility

The current code creates `~/.local/share/quicknote-tui/notes.sqlite` and sets `PRAGMA user_version = 1` after an initial column migration. `notes` contains `id`, `content`, `kill_hash`, `kill_start`, `updated_at`, `created_at`, `deleted_at`; `tags` has `note_id`, `tag`; `notes_fts` indexes content. Some installed databases may have incomplete schema due to ignored `ALTER TABLE` errors. Migration must inspect real table metadata, not assume version 1 implies every column exists.

**Version 2 migration contract:**

1. Open database with bounded busy timeout and verify `integrity_check` or `quick_check` (at least on migration), `foreign_keys`, and schema.
2. Create a durable backup using SQLite online backup/`VACUUM INTO` while not in an active transaction; validate that it can be opened. Show a recoverable error if backup fails. Never migrate without a verified backup on existing data.
3. Migrate in a single transaction; use SQLite `PRAGMA user_version = 2` only after all DDL/data moves succeed. If legacy schemas diverge, reconstruct transactionally instead of blindly applying `ALTER`.
4. Add **at minimum**: `revision INTEGER NOT NULL DEFAULT 0`, `expires_at TEXT NULL`, `is_pinned INTEGER NOT NULL DEFAULT 0`; preserve existing `id`, content, timestamps, `deleted_at`, tags, and FTS indexing. Store timestamps in UTC using a documented RFC3339 representation (or a documented epoch-integer representation chosen before implementation), and use one parser throughout.
5. Backfill `expires_at` from existing `#kill` **only when** `kill_start` and duration can be parsed with high confidence. Treat the last valid directive as authoritative for legacy compatibility. Never silently guess an expiry time when metadata is missing or malformed; instead expose “expiration needs review” and keep the note safe from unintended purge.
6. Add indexes at least on active (`deleted_at`, `updated_at`), expiration (`expires_at` for active rows), trash cutoff (`deleted_at`), and pinned status. Add a uniqueness constraint on `(note_id, tag)` after cleaning duplicates, without deleting notes.
7. Validate FTS row count and repair/rebuild if necessary; FTS5 insertion/update/deletion must remain in the same SQLite transaction as the corresponding note write. Test a database originally created without FTS triggers.
8. If migration fails, roll back, preserve the old file, print a useful recovery path to stderr, and avoid launching in a way that might overwrite data.

**Example destination schema (logical contract; column types/index strategy may be adjusted after tests):**

```sql
CREATE TABLE notes (
    id          INTEGER PRIMARY KEY,
    content     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    deleted_at  TEXT,
    expires_at  TEXT,
    is_pinned   INTEGER NOT NULL DEFAULT 0 CHECK (is_pinned IN (0, 1)),
    revision    INTEGER NOT NULL DEFAULT 0,
    -- legacy columns may be retained for backward compatibility
    kill_hash   TEXT NOT NULL DEFAULT '',
    kill_start  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE tags (
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag     TEXT NOT NULL,
    PRIMARY KEY(note_id, tag)
);
-- FTS5 content index, indexed by note ID; preserve/repair triggers.
-- All indexes and migrations MUST be generated by a versioned migration, not ad hoc at startup.
```

**Concurrency:** `UPDATE notes SET content=?, revision=revision+1, updated_at=? WHERE id=? AND revision=? AND deleted_at IS NULL`; zero affected rows is a conflict or missing note, not a success. Apply the same revision strategy to pin, TTL, delete, restore, tag changes, and concurrent CLI operations to prevent metadata being overwritten by an old draft. If a single writer queue is used, cross-process optimistic concurrency is **still** required.

### 4.2 Lifecycle rules and exact semantics

- **Scratch:** persisted by default; no expiration unless explicitly set. Untitled is valid. Nothing is ever purged just because it lacks a title.
- **Pinned:** persisted and excluded from automatic expiry while pinned. Applying pinning clears/deactivates its existing expiry by default; unpinning does not resurrect an old deadline without confirmation. This avoids immediately deleting a note when unpinned. Manual delete still allowed.
- **Ghost:** created *as memory-only from the outset*, with `ID=0` and an in-memory session UUID; never inserted into DB or indexed. Closing discards it without trash. Toggling an **already persisted** note to ghost must warn that the existing on-disk content cannot be guaranteed erased; safer default is **Duplicate to new ghost and keep original** or explicit **Delete original and continue as ghost** with honest warning. No silent physical-security promise.
- **Trash:** persisted note with `deleted_at` timestamp and `delete_reason` (`manual`/`expired`, or equivalent supported metadata). Recoverable for exactly **72 elapsed hours after deletion**, not merely until a date-boundary 3 days later. Deletion and expiry never hard-delete. A user-selected “Delete forever” is separate and explicitly confirmed.
- **Expiry:** `expires_at` is an absolute UTC instant. On startup, at regular maintenance intervals while open, and before CLI listing/search (when appropriate), expire **all** due active unpinned persisted notes using a single repository operation, not only the current editor note. Idempotent across restarts/processes. An expiration within a dirty open draft must reconcile/flush first and not destroy unsaved content; preserve content as a recoverable draft and warn if needed.
- **Restoring expired notes:** clear expiry by default and leave the `#kill` directive as historical text **only if doing so cannot immediately recreate the deadline**; recommended behavior is to prompt **Restore without timer / Restore and reset timer**. `#kill` parsing must not silently re-arm a canceled timer. An explicit stored TTL mode/override may be needed to distinguish active directive vs canceled directive.
- **Directive semantics:** `#kill 15 mins`, `#kill 2 days`, etc. remain supported. `#kill` is a shorthand to *set* `expires_at` only when the directive is newly added or changed, not on every render, save, or restart. Remove the directive → clear stored expiry only if the expiry was directive-controlled. UI preset and directive precedence must be unambiguous: **last explicit user action wins**, with metadata recording the source (`directive`, `preset`, or none) if necessary.
- **Time zones:** “Today” means local end-of-day in the user's configured system time zone, converted to UTC on persistence, and should behave correctly across DST transitions. Fixed durations use elapsed time. Use an injectable `Clock` for all timing tests.
- **Trash cleanup:** run once on startup and periodically, after the recovery window. Never purge due to content deduplication; check `deleted_at` *strictly older than now − 72h*. Deletion errors are reported. Never make a misleading claim about SSD/NVMe secure wiping or memory remanence.

### 4.3 TTL presets and UX

Open via clickable status-bar TTL, command palette action “Set expiration”, and an accessible configured shortcut. Options: **15 minutes, 1 hour, Today (23:59:59 local), 3 days, 7 days, Never, Custom**. Show the resulting **absolute local date/time**, not just relative wording, before confirmation. Custom supports `15m`, `2h`, `3d`, and a date-time input; validate strictly. Display countdown while open but compute from saved `expires_at`. A time change or clock jump must be reflected on next tick without modifying stored deadlines.

**UI states:** `Saved`, `Saving…`, `Unsaved`, `Save failed — retry`, `Conflict`, `Ghost · unsaved by design`, `Expires in 45m`, `Pinned`, `Trash · recoverable until <date>`. Status labels must not depend solely on color or an icon. Do not show a green “Saved” while an outstanding version remains dirty.

### 4.4 Data portability and privacy

- Never auto-copy content to the system clipboard; offer `Copy note` and optional `Copy selection` only on explicit user intent.
- Explicit plain-text/Markdown file export with safe path handling and informative overwrite confirmation. Use atomic temp-file/rename where feasible, and report errors. Exporting a ghost must warn it creates a persisted file.
- Document backup/restore including tags, TTL, trash, and SQLite WAL handling. Prefer one reliable `quicknote backup --output PATH` command or a documented SQLite backup procedure.
- Ensure debug logs, test snapshots, crash output, and history do not include real note content unless opted in. Provide redaction for error messages.
- Keep the documented distinction: **ephemeral UX** (expires/removes logically), **memory-only mode** (avoids intentional disk writes), and **secure deletion** (not guaranteed). Remove false secure-erasure wording from README.

---

## 5. Editor requirements (E-series)

### E-01 Text operations and Unicode model

- Represent the logical buffer as lines and stable cursor offsets, but define cursor/editor operations over **extended grapheme clusters**. Use an established Unicode grapheme segmenter (the dependency graph already includes `github.com/rivo/uniseg` indirectly; verify actual version/API before adopting), and measure terminal display cells separately with a tested width abstraction.
- Edit APIs (`Insert`, `DeleteBackward`, `DeleteForward`, `ReplaceRange`, `SplitLine`, `JoinLine`, `SetCursor`, `MoveWord`) return updated buffer, cursor, and a reversible edit transaction. No direct string slicing by `cursorCol` outside these helpers.
- Pressing Backspace over `é` (composed or combining form), CJK glyph, `👩‍💻`, or a zero-width-joiner sequence deletes exactly one user-perceived character, without invalid UTF-8.
- Tabs: do **not** silently convert an existing note's `\t` to spaces during load/save. The editor may display a configurable tab width, but preserve the content. Distinguish column of grapheme cluster from rendered cell X coordinate.
- Preserve content including intentional leading/trailing whitespace and final newline, unless the user invokes an explicit trimming/formatting action. Sanitize terminal control bytes for display without silently changing stored note text.
- Support soft-wrap and vertical navigation with a retained preferred visual cell column. Prevent panics on width 1, height 1, empty lines, empty database, or invalid cursor state.

### E-02 Selection, clipboard, and undo

- Shift+arrows extends selection when terminal supports it. Mouse click places cursor; drag selects text where mouse reports permit. Double-click word selection may be added behind compatibility detection. Esc clears selection or closes the active overlay before exiting the app.
- Copy selected text if selection exists, otherwise entire note. Cut must require editable state and participate in undo; paste respects multiline content and plain-text semantics. Fall back to terminal-native paste when clipboard unavailable.
- `UndoHistory` is per draft/note; group continuous typing into transactions (e.g. burst within 500–800 ms), but keep formatting, paste, shell replacements, delete-line, and bulk edits as separate atomic steps. Clear redo after a fresh edit. Bound history by a configurable memory budget and number of transactions.
- Opening a different note preserves its own cursor/scroll/undo state during the session; opening the same note from search restores the correct note-specific stack. Restoring from trash does **not** turn trash view into a text-editing mode for deleted notes.

### E-03 Rendering and code/math behavior

- Preserve existing `math:` and `code:` mode affordances, `=` inline evaluation, Markdown toggles, `:date:`, `:time:`, `:now:`, `:uuid:` expansion, syntax highlighting and safe date/unit arithmetic as backward compatibility. Maintain behavior unless a dedicated tested task explicitly changes it.
- Cache parsed/highlighted spans by `NoteID`, local draft version, theme revision, and viewport width if relevant. An async highlight result must not replace a newer buffer or another note's highlight.
- Render only visible logical/physical lines plus a small overscan margin when notes are long. Prefer incremental layout after edits, with robust simple fallback before complex optimizations.
- Highlight matched spans in searches while preserving legibility against theme backgrounds. Strip or escape unsupported ANSI/control sequences in previews.
- Ensure rendering has no side effects. Map every rendered text span to cell coordinates for mouse hit-testing and accurate search hit jumps.

---

## 6. Discovery, navigation, and search requirements (N-series)

### N-01 Command palette (`Ctrl+K` by default)

The command palette is a centered or top-anchored searchable overlay, accessible even in compact layouts. Contents come from a single typed **action registry**, not a separate hardcoded help list. Each action defines:

```go
type Action struct {
    ID string                // e.g. "note.new", "note.trash", "ttl.set"
    Title string
    Description string
    Category string          // Note, Navigation, Editing, Shell, Settings
    DefaultKeys []string
    Available func(Context) bool
    Enabled func(Context) (bool, string) // reason disabled
    Execute func(Context) tea.Cmd       // or dispatched typed event
    Dangerous bool
}
```

**Required interactions:** Type to fuzzy-find action; arrows/j/k where not conflicting move; Enter runs; Esc closes without editing current buffer; shortcut displayed at right; disabled actions show explanation; recent actions rank higher on an empty query; all actions available by keyboard and mouse if the terminal supports it. Palette always shows current context (e.g. active/trashed/ghost); never allow “Save” on trash or “Restore” outside trash without explanation. Action titles are user-oriented, not internal function names.

**Examples:** New scratch note, New ghost note, Set expiration, Pin/unpin, Find notes, Open trash, Restore, Delete forever, Copy, Export, Pipe through command, Toggle sidebar, Toggle line numbers, Change theme, Toggle animations, Help, List keybindings, Change keyboard profile. Include every existing feature in the registry, not just new ones.

### N-02 Quick switcher (`Ctrl+P` by default)

- Opens a dedicated overlay to move among notes, sorted by recent use/edit with pinned at top where appropriate.
- Typing triggers same search engine as the full finder, but adds a stronger recency/navigation boost. Enter calls `openNote(noteID)`; Esc returns to the unchanged editor draft.
- List entries show title extracted from first appropriate heading or preview fallback, brief matching text, age, optional tags, pin icon/text, and expiration. The title is a derived display label, not a required database field. Dedup by `noteID`, not content.
- The current note is visibly marked; opening it from a result should not reset its undo history or create an unintended save.

### N-03 Full search and filters (`Ctrl+F` retained)

**Pipeline:** parse query → build safe SQLite FTS5 query and indexed filters → fetch bounded candidates → apply bounded fuzzy scoring to relevant title/tag/candidate text → sort and create safe preview → display results. Search is always asynchronous, cancelable/discardable via monotonically increasing request IDs, and visibly distinguishes “No matches”, “Searching”, “Invalid filter”, and “Database unavailable”.

**Query grammar (v2):**

```text
search         := term* filter*
term           := word | "quoted phrase"
filter         := #tag | is:pinned | in:trash | in:active
               | type:json | type:code | expires:today
               | before:YYYY-MM-DD | after:YYYY-MM-DD
```

- Text terms use case-insensitive token matching; by default text terms are ANDed. Quoted phrases search as phrases. `#debug` filters tags, rather than treating it as raw FTS syntax. Multiple tags are ANDed. Filters combine with text via AND. Invalid or contradictory filters show a parser error instead of silently returning zero hits.
- Search defaults to active notes except when invoked inside Trash, where `in:trash` is implicit. An explicit `in:active`/`in:trash` overrides the default. Pinned includes only persisted active notes. Ghost drafts never appear in index/search.
- Prefix search should work (`meet` finds `meeting`); punctuation, quotes, unicode, `*`, parentheses, and other FTS syntax characters must not cause SQL errors or injection. Generate the MATCH expression from validated literal tokens; never interpolate raw query text into SQL.
- Fuzzy search means typo tolerance, not just FTS prefix matches. Include a bounded fallback for candidate titles/tags, e.g., 1–2 character edit distances or subsequence score, and document limits. Avoid scanning full contents of thousands of notes on every keypress; maintain a small lightweight title/tag index or query bounded candidates.
- Rank in order: exact title / tag, exact phrase, prefix, fuzzy title, content relevance, recency (as tiebreak or boost). Stable ties by updated-at then ID. Avoid surprising results solely due to recency.
- **One entry per note** by default; show most relevant matching line/preview and correct noteID + grapheme-aware source range. Optional “show all hits” action may list multiple locations only when requested.
- Empty query shows recent notes and pinned entries. `Ctrl+F` from Trash finds trash entries; opening an item from Trash shows a read-only preview with Restore / Delete Forever actions, not a silently editable active note.

### N-04 Navigation, sidebar, and overlays

- Recent-notes sidebar optional and collapsible; default **hidden** on startup (preserve immediate editor focus). When expanded, show recent, pinned, tag filters, and trash. Current note highlighted. Click entry to open, click tag to filter; keyboard equivalent through quick switcher/command palette and sidebar focus.
- Back/forward note navigation history tracks note IDs independently of sidebar order; next/previous note behavior remains configurable. A history item referencing deleted notes opens read-only trash or is skipped with explanation.
- Use a small consistent overlay stack: editor → palette/search/switcher/TTL/help/confirmation. `Esc` closes topmost overlay first, then selection, then prompts before app exit if dirty/failed-save state exists. Search and palette must not mutate underlying editor on cancel.
- Contextual footer shows 3–5 relevant commands (e.g. `Ctrl+K Commands`, `Ctrl+P Notes`, `Ctrl+F Find`, `F1 Help`) without taking an entire row on tiny terminals. Help is searchable or categorized and generated from action registry.
- Empty states are useful: no notes (“Type to start or Ctrl+N”), empty trash (“No deleted notes”), zero search hits (suggest clearing filters), no tags (explain how to add them), loading/error states clearly distinguished.

---

## 7. Visual and interaction specifications (V-series)

### 7.1 Layout matrix

| Available size | Default layout | Constraints |
|---|---|---|
| ≥ 100 columns, ≥ 20 rows | Editor with **optional** sidebar (20–28 cells) and one-row status/footer; overlay centered | Sidebar does not steal more than ~30% width. |
| 70–99 columns | Editor-only by default; sidebar opens as temporary overlay | Command/search menus fit without horizontal clipping. |
| 40–69 columns | Compact editor, minimal gutter, one-line status; sidebar replaced with picker | Titles and previews truncate by display cells, not bytes. |
| < 40 columns or < 12 rows | Minimum viable edit view; overlay becomes full-width compact list | Never panic, never emit offscreen mouse targets. |
| Terminal ≤ 5 rows | Bare editor with minimal status or temporary action prompt | All core commands still invocable. |

Never assume an 80-column terminal when actual width is smaller. `WindowSizeMsg` must be handled from any overlay, including mouse hit-map recreation. Decorations cannot obscure content or prevent typing. Respect local scroll position as window size changes.

### 7.2 Visual design tokens and themes

- Semantic roles: `background`, `surface`, `surfaceRaised`, `text`, `textMuted`, `border`, `accent`, `selection`, `match`, `success`, `warning`, `danger`, `focus`, `code`, `math`.
- Ship built-in **Catppuccin Mocha, Macchiato, Frappé, Latte** and a basic monochrome fallback. Names and palette values should be taken from current canonical Catppuccin definitions when implementing, not made up from memory.
- User config merges **per token** over a complete validated base theme. Invalid hex, missing tokens, and unsupported names produce a gentle error and fallback, not invisible text.
- Terminal capability detection: true color, 256-color, 16-color, and monochrome; no critical meaning conveyed by color alone. Nerd Font glyphs optional; standard Unicode/ASCII replacements for icons and powerline separators.
- Rendering hierarchy: content first; thin accent for active note; distinct but subtle focus; reserved red for destructive and expiring states; short status toasts; rounded borders only when terminal rendering supports them.
- Theme picker has live preview and cancel-to-revert, with persisted choice on explicit confirmation. Avoid writing configs on every keypress.

### 7.3 Mouse map and accessibility

- Layout pass emits `{x,y,width,height,actionID}` hit regions. Use the **same layout calculation** for rendering, cursor hit-testing, and mouse clicks. Regions have a z-order (dialogs before editor), and offscreen regions cannot receive events.
- Click editor = place cursor; wheel = scroll; click sidebar row/tag/status controls = corresponding action; click dialog action = activate. Drag-select where supported; gracefully allow native terminal selection when not capturing mouse or when user temporarily disables app mouse mode.
- Use tooltips sparingly; hover is not reliable in terminal clients. Put shortcut and accessible text in the action palette, help, and status. Right-click context menu is optional depending on terminal reporting; its absence must not block commands.
- Keyboard focus is visible. If mouse reporting fails, all UI is still usable. CLI flag/config to disable mouse support entirely.

### 7.4 Animations, effects, and notifications

- Offer only **short event-driven transitions** for opening overlays, switching notes, and save/delete notifications, usually 80–150 ms, **off by default on constrained/remote terminals or in `reduce_motion` mode**.
- Motion may not shift text under the cursor, reduce edit throughput, or trigger continuous 60 FPS redraw when idle. No background particles or expensive per-character animation.
- Prefer small slide/fade-like terminal effects (progressive border/opacity simulation) if they benchmark well; no real transparency or blur claim in a TUI. Rely on terminal theme transparency for blur, which the app does not control.
- Error toasts must remain visible until acknowledged or long enough to read; transient success confirmations can time out. A save conflict is persistent, not a disappearing toast.

### 7.5 Keyboard map and intentional compatibility change

| Action | Default v2 | Current/baseline | Notes |
|---|---|---|---|
| Command palette | `Ctrl+K` | **Delete line** | Intentional conflict. New default remaps delete line to `Alt+K`; offer `legacy` keyboard profile preserving Ctrl+K delete line, with palette on a configurable alternative. |
| Quick switcher | `Ctrl+P` | Not verified as assigned | Must test interception by terminals/tmux. Configurable. |
| Full note search | `Ctrl+F` | `Ctrl+F` | Preserve. |
| Create note | `Ctrl+N` | `Ctrl+N` | Preserve. |
| Open trash | `Ctrl+T` | `Ctrl+T` | Preserve. |
| Restore note | `Ctrl+R` in Trash | Same | Preserve context. |
| Ghost mode | Palette / configured shortcut; `Ctrl+G` legacy | `Ctrl+G` | Ghost conversion requires safety semantics (§4.2). |
| Export | `Ctrl+S` | `Ctrl+S` | Preserve existing “Save to file” meaning; auto-save remains separate. |
| Undo/redo | `Ctrl+Z` / `Ctrl+Y` | Same | Preserve. |
| Shell pipe | `Alt+X` | Same | Preserve, add review screen. |
| Copy | `Alt+C` | Same | Preserve. |
| Help | `F1` | `F1` | Preserve; make searchable. |
| Line numbers | `F2` | `F2` | Preserve. |
| Next/previous note | `Alt+→` / `Alt+←` | Same | Preserve; route through openNote ID. |
| Set expiry | Palette / status click | `#kill` directive | Keep directive working. |
| Pin/unpin | Palette / sidebar click | New | Any default shortcut must pass keymap collision checks. |

Implement `keymap.validate()` to detect duplicate global chords **and** modal precedence ambiguity; allow user-defined keys in a config file. Document terminal limitations (e.g. Ctrl+Shift combinations may be indistinguishable, tmux may intercept keys). Migration notice on first v2 launch lists changed `Ctrl+K` and how to select the legacy profile, without blocking typing.

---

## 8. Shell, CLI, export, and integration requirements (I-series)

### I-01 Shell pipeline

- Shell execution only after explicit `Alt+X` or palette action. Initial prompt displays which content will be filtered (entire note or selected text) and a short preview. Default current behavior of protecting Markdown heading/`code:`/`math:` metadata can be retained as a **named scope** (`Body excluding metadata`) but should be shown, not hidden.
- Run asynchronously with `context.WithTimeout` (suggest 10 s default, configurable); pass content as stdin, never interpolate content into the command string. Use the user's chosen local shell only when explicitly requested; default `sh -c` is allowed for user-supplied commands. No network restrictions promised for shell commands because the user's shell has the user's OS permissions.
- Capture bounded stdout/stderr separately (e.g. configurable 1–10 MiB cap), show exit status and errors. On failure or timeout, preserve original note unchanged. On success, open a **diff/preview confirmation**: Replace body / Insert below / Copy output / Cancel. Replacement is one undo transaction.
- A command finishing after the user has switched or edited notes must be rejected as stale or shown as a separate result requiring deliberate apply. Never replace whichever note happens to be active.
- Expose a `Cancel` action while process is running and an optional command history **without writing secrets or note contents to logs**. Avoid accidental repetitions of dangerous commands.

### I-02 CLI contract

Preserve documented invocations:

```bash
quicknote                                  # interactive, recent persisted note
quicknote -n 'check staging logs'          # create new persisted note
printf 'raw data\n' | quicknote -q         # capture silently
printf '{"a":1}\n' | quicknote -t 'API'  # title/append behavior
quicknote -l -n 'follow up'               # append to latest active note
quicknote -c 'date'                       # capture explicitly run command
```

- `-q`: no success output to stdout; diagnostics go to stderr and nonzero exit status. Consider a separate `--verbose` flag for informative successes.
- `-n`, `-l`, and `-t` must use the same repository/CAS and tag/expiry path as TUI saves. Define precedence for incompatible flags and reject invalid combinations. Preserve existing title-append behavior unless an explicit deprecation warning is documented.
- Read stdin with a configured size cap and correct error handling. Preserve literal input bytes/whitespace by default; support an explicit trim option if desired. Avoid unnecessary blocking reading from a terminal.
- When stdin is piped and no interactive `/dev/tty` exists, either honor `-q` or report an actionable error (do not launch a broken TUI). Opening a terminal may be skipped in quiet mode.
- Optional v2 subcommands (`list`, `search`, `show`, `trash`, `restore`, `backup`) are **P2** and only after the shared service and data model stabilize; do not delay core reliability for CLI feature growth.

### I-03 Clipboard, editor, desktop integration

- Choose supported backend in order based on environment and availability: native Wayland `wl-copy`, X11 `xclip`, macOS `pbcopy`, then OSC 52 when terminal permits; offer clear copy-failure status and keep external commands bounded.
- Test Wayland + Kitty, Wayland + tmux, local X11, SSH/tmux where practical. OSC 52 behavior must be opt-in/configurable because terminals differ.
- Define `quicknote --floating`/launcher or a sample desktop `.desktop`/shell wrapper only as optional documentation; **do not implement a new compositor or rely on desktop-specific APIs**. Integrate cleanly with the user's terminal (including floating terminal launch scripts if desired).
- Export file names are derived safely from headings, but never trust a title as a path. Explicit overwrite prompt and directory/file permission error handling required.

---
## 9. Reference wireframes and behavior (informative, not pixel-perfect)

The goal is **consistent interaction**, not visual fidelity to these ASCII sketches. No implementation should hardcode widths, colors, or Nerd Font glyphs based only on these examples.

### 9.1 Editor, wide terminal (sidebar optional)

```text
 quicknote / scratchpad                     [Saved]  [Expires in 45m]
 ──────────────────────────────────────────────────────────────────────
 RECENT                    | # API response
 > API response       now  | 1  ```json
   Build ideas       8m    | 2  {
   Meeting notes     3h    | 3    "status": 200,
 ─────────────────────────  | 4    "success": true
 PINNED                    | 5  }
   Remember this           | 6  ```
 TRASH (3)                 |
 ──────────────────────────────────────────────────────────────────────
 Ctrl+K Commands · Ctrl+P Notes · Ctrl+F Find · F1 Help      Ln 6, Col 1
```

**When sidebar is hidden**, editor expands horizontally and starts focused. Do not add a mandatory splash screen.

### 9.2 Command palette

```text
          ┌ Commands · type an action ───────────────────────────┐
          │ > pipe_                                             │
          ├─────────────────────────────────────────────────────┤
          │ > Pipe note through a command        Alt+X          │
          │   Copy selected text                  Alt+C          │
          │   Export note to file                 Ctrl+S         │
          └ Enter run · Esc close ───────────────────────────────┘
```

### 9.3 Search / quick switcher

```text
          ┌ Find notes ─────────────────────────────────────────┐
          │ #debug type:json  repsonse_                          │
          ├─────────────────────────────────────────────────────┤
          │ > API response    3m ago  #debug  · ..."status":... │
          │   Staging output  1h ago  #debug  · ..."payload"... │
          └ ↑↓ choose · Enter open · Esc close ─────────────────┘
```

Misspelling in the query is deliberately shown to exercise fuzzy title matching; literal content search need not match arbitrary typos if candidate bounds are explicit. Search suggestions should not falsely imply fuzzy matching can find every typo anywhere in a long note.

### 9.4 Saving and destructive actions

- **Clean:** `Saved 11:47` or `Saved`. **Edit pending:** `Unsaved`. **Async operation:** `Saving…`. **Failure:** `Save failed — Retry [Enter] / Save copy / Details`. **Conflict:** `Changed in another session — Inspect / Keep both / Reload`.
- Delete persisted note: move to trash immediately with short toast `Moved to Trash · Undo`. Undo restores same note ID until purge; it must not be a second duplicate note.
- Delete Forever: `Permanently delete this note? This cannot be undone by Quicknote.` Default focus **Cancel**; type-confirm optional for entire trash purges.
- Search cancel: returns to exactly the previous note, buffer, cursor, scroll, and save state; no note switch or surprise save caused solely by opening the overlay.

---

## 10. Test plan and acceptance scenarios

### 10.1 Testing layers

1. **Unit / property tests:** Unicode buffer edits, cursor-cell mapping, tag/directive parsing, query parsing, fuzzy ranking, keymap collision detection, duration parsing, theme validation, action availability. Avoid terminal or filesystem dependencies when possible.
2. **Repository integration:** SQLite temporary database in `t.TempDir()`, migrations from version 0/1 fixtures, FTS trigger consistency, tags, deleted timestamps, concurrent compare-and-swap, interrupted transactions, maintenance idempotency.
3. **Bubble Tea model tests:** Inject messages and a fake Store/Clock, then assert `NoteID`, buffer, dirty state, overlays, undo stack, pending jobs and save result reconciliation. Test stale out-of-order async completions explicitly.
4. **PTY / golden render tests:** Terminal resize, no wrap overflow, compact palettes, mouse coordinates and hit regions, ANSI safety, color fallback; normalize timestamps/colors for stable snapshots.
5. **Manual interaction tests:** Kitty, tmux, Wayland clipboard, mouse capture and terminal selection, key interception, shell pipelines. Record versions and limitations.
6. **Benchmark tests:** Warm/cold startup, large-note rendering, search corpus, save throughput and allocations. Never claim improvement without comparable before/after measurement.

### 10.2 Canonical regression catalog

| Test ID | Setup and action | Expected result |
|---|---|---|
| T-01 | Create notes A=`alpha`, B=`beta`; open B from Ctrl+F while A active, append `!`, autosave, reload both from DB. | A remains `alpha`; B is `beta!`; active NoteID is B throughout. |
| T-02 | Start save for A, immediately open B, then deliver A's save completion and older B highlight. | B buffer remains B; no cross-note save/status/highlight corruption. |
| T-03 | Edit A; switch to B; undo; return to A; undo. | B undo cannot pull A content; A undo recovers its own prior content. |
| T-04 | Type `café`, `e\u0301`, `你好`, `👩‍💻`; move, backspace, insert backtick/fenced code, wrap and click. | No panic, invalid UTF-8, partial grapheme edits, or misplaced cursor. |
| T-05 | Trash two separate notes with identical content; restart; restore each by ID within 72h. | Both are independently recoverable with their original content/tags. |
| T-06 | Force SQLite write/tag failure (read-only/failed transaction); edit note and save. | Error surfaced; dirty retained; DB atomically unchanged; retry possible. |
| T-07 | Create expiring note; close app; advance fake clock past expiry; invoke startup and CLI list. | Note moves to trash once; no need to open note; recoverable for 72h. |
| T-08 | Open expired note + another note; render `View` twice without Update. | No DB writes, timer changes, or model mutation; same output. |
| T-09 | Search `foo*`, `"quote`, `(broken`, emoji, `#debug`, quoted phrase. | No SQL error swallowed; valid literal search or explicit query-grammar error. |
| T-10 | Corpus containing `response`, `respones`, `respnse`; query expected fuzzy result with a typo. | Bounded typo-tolerant title/tag matches, stable ordering, correct source preview. |
| T-11 | Run slow/failed shell filter; type, resize, cancel and switch notes before result. | UI stays responsive; canceled/failed jobs do not modify text; late output cannot affect other notes. |
| T-12 | Pipe content to `quicknote -q`, redirect stdout/stderr, simulate DB error. | Success stdout empty and exit 0; failure stderr meaningful, nonzero exit. |
| T-13 | Render `View` with fake store instrumented for I/O and a countdown. | Zero store calls and state mutations, including no `KillStart` changes. |
| T-14 | Invalid partial theme JSON; switch truecolor/256/16/mono; tiny resize. | Valid defaults merged; visible text, no panic/overflow. |
| T-15 | Import note from stdin containing leading spaces, trailing blanks, tabs, and newline. | Identical persisted payload unless explicit trim option supplied. |
| T-16 | Ctrl+K in default profile, Alt+K delete-line, Ctrl+K in legacy profile. | Palette vs line-delete semantics match profile; help and palette show correct keys. |
| T-17 | Simultaneous processes edit same note at same revision and save conflicting content. | Exactly one succeeds; other sees conflict; both versions can be recovered or copied. |
| T-18 | Migration fixture with version-1 notes/tags/trash/FTS; migration succeeds; run a second time. | All IDs/content/tags/timestamps preserved; v2 idempotent; indexes functional; backup exists. |
| T-19 | Corrupt/partial old schema or simulated migration failure. | Transaction rolls back; original recoverable; no false schema version advance. |
| T-20 | TTL presets across DST/local midnight; restart; pin/unpin; remove `#kill`. | Correct absolute UTC deadline; pin prevents expiry; removed directive clears only its own timer. |
| T-21 | Restore expired note with `#kill`, choose cancel/restart timer variants. | No immediate re-expiry without explicit choice; countdown consistent afterward. |
| T-22 | Create ghost from New Ghost action; enter content; open switcher/search; exit/restart. | No ghost data written to notes, tags, FTS, exports, or logs; ghost absent after restart. |
| T-23 | Transform existing persisted note to ghost. | Warning before conversion; existing disk persistence not misrepresented as securely erased. |
| T-24 | Search quickly `a`, `ab`, `abc`, deliberately complete responses out of order. | Only latest query results display; pressing Enter opens correct noteID. |
| T-25 | Resize through 120×40, 80×24, 50×12, 25×5; open every overlay. | No panic, invisible focus, broken clipping, or unreachable cancel action. |
| T-26 | Click sidebar note/tag, editor wrapped line, status TTL, and overlay action. | Correct hit region invoked, no off-by-one including double-width characters. |
| T-27 | Disable mouse; navigate all commands, TTL presets, Trash, search, export with keys only. | 100% core functionality available by keyboard. |
| T-28 | Trigger save failure/conflict/expiry with success/error toasts. | Text labels remain understandable in mono mode; errors persist until handled. |
| T-29 | Export to writable, existing, and permission-denied paths. | Correct content and overwrite choice; visible errors; no truncation on failure. |
| T-30 | Pipe content containing `; rm -rf` *as data* through benign `cat` command. | Content stays data, never becomes shell source. (The user-entered command itself is intentionally executed.) |
| T-31 | Two delete operations followed by Undo and eventual timed purge. | Restore works within 72h; purge only after threshold and is idempotent. |
| T-32 | A 100k-line note with syntax highlighting and line wrapping; scroll/edit/search. | Responsiveness meets benchmark gates; no full-text recompute on every scroll if not needed. |
| T-33 | Input with terminal escape sequences and control codes. | No hostile terminal output/cursor manipulation through previews; raw content is preserved or sanitization documented. |
| T-34 | Clipboard unavailable inside tmux/SSH; copy with fallback disabled/enabled. | Failure indicated or supported fallback works; never silently claim success. |
| T-35 | Re-run math and code-mode operations, snippets, heading extraction, and date/units. | Existing supported behavior preserved; changes tested and documented. |

**Property/fuzz testing recommendations:** fuzz `EditorBuffer` inserts/deletes against valid grapheme boundaries; fuzz text/term parser to ensure it never panics; fuzz `getKillTimerInfo` and filter parser for invalid values, huge durations, overflow, negative/zero TTL, and arbitrary Unicode. Use injected clock for all 72-hour and DST boundaries. Where terminal behavior varies, report the exact supported environment rather than pretending a single golden test proves compatibility everywhere.

### 10.3 Required database fixture cases

- Empty new database, legacy version-0 database, legacy version-1 database, legacy version-1 with missing FTS table, legacy partial ALTER state, expired note, duplicate-content notes, orphan tags, unusual UTC timestamps, malformed `kill_start`, concurrently updated note, read-only directory, locked SQLite busy timeout.
- Use synthetic notes only; store fixtures in `tests/fixtures` and ensure they contain no personal notes. Always check SQLite foreign keys, `integrity_check`, FTS consistency, note counts, and tags after migration.

---

## 11. Performance and reliability budgets

These numbers are **release goals, not measured properties of current Quicknote**. Record hardware, OS, Go version, terminal, database size, and sample distribution. Run at least 5 independent trials for cold starts; compare medians and 95th percentile where sensible.

| Scenario | Suggested target | Measurement method |
|---|---|---|
| Interactive cold start, 100 notes | ≤ 100 ms median to first usable editor | PTY/script timing or instrumentation from process start to first rendered editor-ready state; specify cache effects. |
| Key-to-render, 5 KB note | p95 ≤ 16 ms on reference workstation | Inject keystrokes, timestamp model/render; no blocking DB/shell inside event loop. |
| Warm indexed search, 10k notes | ≤ 50 ms p95 for candidate retrieval + ranking, excluding terminal render | `go test -bench` with reproducible generated corpus and prepared index. |
| Scroll 100k-line note | No visible stalls; p95 frame ≤ 33 ms with viewport rendering | Bench render at multiple widths; measure allocations. |
| Memory use, 10k note summaries | Remain bounded and avoid loading every full note into RAM | RSS/allocation test; define numerical budget after baseline. |
| Idle UI | No needless continuous redraws/high CPU | Instrument tick rate and CPU with no active edits. |
| Autosave correctness | 0 lost newest revisions across 10k randomized edit/save/switch cycles | Model test with deterministic out-of-order completion + SQLite checks. |
| Retention correctness | 100% of persisted normal deletes recoverable within 72h | Fixture/property tests with injected clock. |

If targets fail, first capture profiling data (`pprof`, allocations, SQL `EXPLAIN QUERY PLAN`). Optimize based on measurement, not on speculative low-level rewrites. Performance is subordinate to safety/invariants: never eliminate revision checks, backups, or error handling merely to make a benchmark look faster.

---

## 12. Verification gates, CI, and release policy

### 12.1 Before merging any task

```bash
gofmt -w <changed Go files>
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...   # mark NOT RUN if toolchain unavailable
go build ./...
git diff --check
```

- Migrations/store changes additionally run all fixtures and `PRAGMA integrity_check`; show backup/recovery verified with a real round trip.
- Editor/search changes additionally run fuzz/property or model tests at least in a bounded smoke mode.
- UI changes additionally run golden layout at 120×40, 80×24, 50×12, and 25×5 and a manual keyboard pass if an interactive terminal is available.
- CLI/integration changes additionally exercise stdout/stderr/exit status, negative cases, and shell timeouts.
- Use CI matrix for current supported Go release(s) and Linux; consider macOS build if practical. `go-sqlite3` requires CGO/GCC for normal builds; document this and avoid claiming pure-Go cross-compilation. Pin action versions and set minimum privileges.

### 12.2 Definition of done for each task

A task is `DONE` only if implementation is merged/committed, requirements and edge cases pass, new tests exist for the failure mode, old behavior remains compatible or has a documented migration, code is formatted, errors are handled, and `IMPLEMENTATION_PROGRESS.md` includes verification evidence. UI items additionally require a screenshot or textual snapshot description and a keyboard-only route; storage items require old-data fixture tests.

### 12.3 Release gates

**Alpha (after Milestones 0–2):** P0 issues closed; no known cross-note data loss; migrations and expiration are recoverable; search works with correct IDs; app starts and basic shortcuts work.

**Beta (after Milestones 3–4):** Command palette, quick switcher, sidebar, TTL picker, four themes, responsive UI, keymap migration, mouse regions and contextual help usable; manual platform matrix in progress.

**v2 stable (after Milestones 5–6):** All non-deferred P0/P1 tasks complete; full regression catalog; documented shell/CLI/error behavior; benchmark record; packaging/CI; release notes, migration backup instructions, updated README screenshots and truthful privacy/performance claims.

Keep an explicit compatibility note for changing `Ctrl+K` default, Ghost conversion semantics, save/conflict handling, input whitespace, and any changed expiration logic. An unfinished P2 feature is not permission to claim v2 stable lacks any documented core behavior.

---

## 13. Delivery roadmap and ordering

Do **not** equate a “milestone” with one coding session. Aim for one focused task (occasionally two tiny adjacent tasks) per session/PR. Dependencies in §14 are authoritative; overlapping modules can be advanced independently once prerequisites pass.

| Milestone | Scope | Entry condition | Exit gate | Rough solo-developer estimate |
|---|---|---|---|---|
| **M0: Safety baseline** | Test harness, search identity, per-note history, Unicode, trash, error handling, pure `View`, CLI quiet | Existing `main` builds | No known P0 cross-note/retention bug; reproducible regression suite | 4–8 focused days |
| **M1: Data and core architecture** | Store interface, transaction/revision model, migration v2, typed async jobs, editor buffer contracts | M0 safety tests | Old DB migrates safely; save conflicts cannot silently overwrite content | 6–10 days |
| **M2: Lifecycle** | Persistent TTL, pinning, ghost-from-creation, trash restoration, presets | M1 schema and store done | All lifecycle and restart tests pass; clear privacy wording | 5–8 days |
| **M3: Search/navigation/discoverability** | Query parser, FTS5 + fuzzy, previews, palette, switcher, sidebar, nav history, help | M1 stable ID/events | Search is reliable and useful; all actions discoverable without memorized shortcuts | 7–12 days |
| **M4: Visual polish and mouse** | Layouts, Catppuccin, color fallback, hit maps, selection, status, restrained animations | M1 events; M3 action registry | Keyboard-only, mouse, compact, mono modes pass | 5–9 days |
| **M5: Terminal & shell integration** | Safe async pipe, clipboard, export, CLI service, optional desktop launch | M1 store and commands | End-to-end CLI/shell tests; no blocking UI jobs | 4–7 days |
| **M6: Release hardening** | CI, benchmarks, profile optimization, docs, compatibility and packaging | P0/P1 features integrated | Alpha/Beta/stable release gates | 4–8 days |

Estimates are **not** promises or a schedule; make decisions based on failing tests, not deadlines. Some task work naturally overlaps milestones. Work should be paused if a new data-loss bug appears, until repaired.

---
## 14. Atomic task ledger (the implementation work queue)

**Ordering:** P0 before P1 before P2, provided prerequisites are fulfilled. All tasks start `TODO` in this original spec. Update current statuses in `IMPLEMENTATION_PROGRESS.md`, **not** by rewriting accepted requirements to match an incomplete implementation. “Pass” below means test evidence, not opinion. Task ranges are stable even if work is split into additional PRs.

### M0 — Safety baseline and regression isolation

| Task | Priority | Dependencies | Deliverable, exact acceptance |
|---|---|---|---|
| **QN-001** Establish baseline tests | P0 | — | Record SHA, versions, current test failures; create synthetic SQLite fixtures and model-test seams. `go test ./...` runs; no real user DB used. (§2.3, §10) |
| **QN-002** Fix search-to-editor identity | P0 | 001 | Selecting a search hit switches authoritative noteID, not just buffer. Write identity-based `openNote` shim. **T-01, T-02 pass**. |
| **QN-003** Isolate undo/save/navigation state | P0 | 002 | Note-specific undo/redo, cursor, viewport and dirty save state; switching preserves drafts and undo isolation. **T-03 passes**. |
| **QN-004** Unicode/UTF-8 safe editing | P0 | 001 | Remove byte/rune indexing mismatch (`backtick` and similar), add grapheme-safe operations and display-map tests. **T-04 passes**. |
| **QN-005** Preserve duplicate notes in trash | P0 | 001 | Remove duplicate-hard-delete behavior and destructive dedup cleanup; trash retention 72h independent of content. **T-05, T-31 pass**. |
| **QN-006** DB error propagation + transactions | P0 | 001 | Fix ignored save/tag/delete/restore errors; atomic note+tag writes; dirty note stays retryable on error. **T-06 passes**. |
| **QN-007** Pure rendering and timer side effects | P0 | 001 | Remove `View` mutation, I/O, and timer resetting. **T-08, T-13 pass**; output deterministic. |
| **QN-008** CLI quiet/error baseline | P0 | 001 | Fix `-q` stdout contract, stdin error checks, and nonzero failures without changing intended flags. **T-12 passes**. |

### M1 — Architecture, data, and editor stabilization

| Task | Priority | Dependencies | Deliverable, exact acceptance |
|---|---|---|---|
| **QN-009** Extract typed Store API | P1 | 002, 005, 006 | Move SQLite behind interface, centralize CRUD, tags and data errors; no TUI direct SQL. All existing store tests pass. (§3.3) |
| **QN-010** Version-2 schema + backup migration | P1 | 009 | Implement `revision`, `expires_at`, `is_pinned`, safe legacy repair, backup, indexes, FTS consistency, idempotency. **T-18, T-19 pass**. |
| **QN-011** Compare-and-swap concurrency | P1 | 010 | Optimistic revision for TUI/CLI writes and metadata; conflict preservation UI/message contract. **T-17 passes**. |
| **QN-012** Async commands with stale result protection | P1 | 011 | Typed messages and generation IDs for save/load/search/highlight; no DB operations on typing event loop. **T-02, T-24 pass**. |
| **QN-013** EditorDraft and authoritative NoteID | P1 | 003, 004, 012 | Replace index-driven note state with explicit Draft/NoteID; per-note history, source maps, save and open transition contracts. **T-01–04 and T-17 pass**. |
| **QN-014** Central action registry | P1 | 013 | Registry for **all** existing and new commands, context, enabled state and descriptions; unit tests cover actions. (§6 N-01) |
| **QN-015** Versioned configurable keymap | P1 | 014 | Default/legacy profiles, Ctrl+K collision handling, Alt+K line-delete, help generated from registry. **T-16 passes**. |
| **QN-016** Shared responsive layout engine | P1 | 013 | Terminal dimension logic, wrapping with grapheme/cell model, focused editor, pure viewport layout. **T-25 passes**. |
| **QN-017** Overlay stack and confirmation system | P1 | 014, 016 | Modal states with consistent Escape/focus/cancel; reversible actions and error dialogs; no buffer mutation on cancel. (§6 N-04, §9.4) |
| **QN-048** Preserve math/code/snippets | P1 | 004, 013 | Add compatibility tests for math, code, snippets, Markdown toggles, headings, units/dates; fix regressions during refactor. **T-35 passes**. |

### M2 — Lifecycles and recoverability

| Task | Priority | Dependencies | Deliverable, exact acceptance |
|---|---|---|---|
| **QN-028** Durable TTL engine | P1 | 010, 011, 012 | Persist UTC `expires_at`, import valid legacy directives, startup/periodic all-note expiry, injection clock, directive precedence. **T-07, T-20 pass**. |
| **QN-029** TTL picker and status countdown | P1 | 028, 014, 017 | 15m/1h/today/3d/7d/never/custom, local confirmation, absolute UTC storage, accessible status click and palette action. **T-20 passes**. |
| **QN-030** Pinning | P1 | 028 | Pin/unpin stored atomically; pin prevents auto-expiry; unpin confirms reset behavior; sidebar/command support. **T-20 passes**. |
| **QN-031** Ghost-from-creation semantics | P1 | 005, 010, 013 | New ghost avoids persistence/index; safe conversion of existing notes; accurate privacy messaging. **T-22, T-23 pass**. |
| **QN-032** Trash UI and restore rules | P1 | 005, 017, 028 | Read-only trash preview, restore expired note without instant re-expiry, undo move-to-trash, permanent delete confirmation. **T-21, T-31 pass**. |

### M3 — Search, navigation, and discoverability

| Task | Priority | Dependencies | Deliverable, exact acceptance |
|---|---|---|---|
| **QN-018** Filter/query grammar | P1 | 009 | Parser for literals, phrases, tags, scope/date/type filters; rejects malformed filters safely. **T-09 passes**. |
| **QN-019** Safe indexed search backend | P1 | 010, 018 | Escaped FTS5, proper query plans, stable NoteIDs/source offsets, errors surfaced. **T-09 passes**, SQL benchmark documented. |
| **QN-020** Bounded fuzzy ranking | P1 | 019 | Typo-tolerant title/tag matching, fast candidate ranking and stable ties; no full-text scan on every key. **T-10 passes**. |
| **QN-021** Search results and snippets | P1 | 013, 020 | One hit/note, actual highlighted match, context preview and navigation; trash scoped read-only. **T-01, T-24 pass**. |
| **QN-022** Quick switcher | P1 | 015, 017, 021 | Ctrl+P recent/pinned fuzzy selection, open by NoteID, cancel has no side effects. **T-01, T-03 pass**. |
| **QN-023** Command palette | P1 | 014, 015, 017 | Ctrl+K action fuzzy filter, contextual enable state, keyboard/mouse controls and displayed bindings. **T-16, T-27 pass**. |
| **QN-024** Optional recent/tags sidebar | P1 | 016, 021, 013 | Collapsible sidebar with recent/pinned/tags/trash, no required startup panel; responsive hidden modes. **T-25, T-26 pass**. |
| **QN-025** Tag filters and navigation | P1 | 018, 020, 024 | Clicking/typing `#tag` filters correct note set; tag metadata matches persisted content and ignores code spans as specified. **T-09 pass**. |
| **QN-026** Searchable contextual help | P1 | 014, 023 | Help and footer generated from action registry/keymap; disabled actions explained; no stale shortcut text. **T-16, T-27 pass**. |
| **QN-027** Note navigation history | P2 | 013, 022 | Back/forward based on IDs with deleted/missing-note handling; restores viewport without cross-note writes. **T-02, T-03 pass**. |

### M4 — Visual design and mouse controls

| Task | Priority | Dependencies | Deliverable, exact acceptance |
|---|---|---|---|
| **QN-033** Theme system and Catppuccin variants | P1 | 016 | Mocha/Macchiato/Frappé/Latte, token validation/merging, capability fallback, previews. **T-14 passes**. |
| **QN-034** Incremental syntax highlight/render | P1 | 004, 013, 016 | Correct highlighted spans after edits/switches, viewport rendering cache with revision keys. **T-02, T-32, T-33 pass**. |
| **QN-035** Mouse hitmap + clickable controls | P1 | 016, 017, 024 | Shared render/hit coordinates, click sidebar/TTL/palette, correct cell positions, optional disable. **T-26, T-27 pass**. |
| **QN-036** Selection and clipboard | P1 | 004, 035 | Keyboard/mouse selection, plain-text copy/cut/paste, explicit clipboard backends and errors. **T-04, T-34 pass**. |
| **QN-037** Restrained UI transitions | P2 | 016, 033 | Timed nonblocking overlay and navigation motion, reduce-motion setting, measured no-idle-redraw. **T-25 and perf idle gate pass**. |
| **QN-038** Status, notifications, error UX | P1 | 014, 017, 028 | Visible Saved/Saving/Failed/Conflict/TTL/Ghost/Trash statuses; persistent error/confirm path. **T-28 passes**. |
| **QN-042** User config and settings UI | P2 | 015, 033 | Config defaults, validation, keymap/theme/mouse/motion options; safe atomic persistence and documented location. **T-14, T-16 pass**. |

### M5 — Shell, files, and existing CLI integration

| Task | Priority | Dependencies | Deliverable, exact acceptance |
|---|---|---|---|
| **QN-039** Safe asynchronous shell pipeline | P1 | 012, 013, 017 | Local explicit shell runner, output bounds/timeout/cancel, confirmation/diff, undo, stale-response protection. **T-11, T-30 pass**. |
| **QN-040** Shared CLI/storage behavior | P1 | 009, 011, 028 | CLI uses Store and common lifecycle, robust errors, title append, quiet stdout, stdin preservation. **T-12, T-15 pass**. |
| **QN-041** Export/backup/restore | P1 | 006, 009, 017 | Atomic safe file export, overwrite/permission errors, SQLite backup/restore docs or CLI. **T-18, T-29 pass**. |
| **QN-047** Optional enhanced CLI commands | P2 | 040, 041 | Add `list/search/show/restore/backup` only if maintainer accepts scope; scripts have stable stdout/exit contracts. Dedicated integration tests. |

### M6 — Validation and publishing

| Task | Priority | Dependencies | Deliverable, exact acceptance |
|---|---|---|---|
| **QN-043** CI, lint, race, migration jobs | P1 | 001, 009 | GitHub Actions build/test/vet/race (supported environment), synthetic fixture migration and format check. Merge gates enforced. (§12) |
| **QN-044** Benchmark suite and optimization | P1 | 016, 020, 034 | Reproducible corpus, baseline and after figures, pprof where targets missed; publish results without overclaims. **T-32 passes** and §11 measured. |
| **QN-045** Platform compatibility matrix | P1 | 033, 035, 039, 040 | Kitty/tmux/Wayland and other terminals recorded, text fallback, key/clipboard/size/mouse/manual pass. **T-25–27, T-34 pass**. |
| **QN-046** Documentation and v2 release | P1 | 043, 044, 045, all non-deferred P0/P1 | README, help, screenshots, installation with accurate Go requirement, privacy/performance claims, migration and recovery guide, changelog/tag/build. All §12.3 gates pass. |

### 14.1 Dependency critical path and parallel lanes

```text
001 ── 002 ── 003 ──┐
 │      │           ├─ 013 ─ 014 ─ 015 ─ 023 ─ 026
 ├─ 004 ────────────┤          └─ 016 ─ 017 ─ 024 ─ 035
 ├─ 005 ──┐         │
 └─ 006 ──┴─ 009 ─ 010 ─ 011 ─ 012 ─┘
                   ├─ 028 ─ 029/030/032
                   └─ 018 ─ 019 ─ 020 ─ 021 ─ 022

Other lanes after foundations:
  031 ghost     033 themes     034 render     036 clipboard
  038 status    039 pipeline   040 CLI        041 export
  043 CI        044 perf       045 platforms  046 release
```

Dependency **table**, not diagram, governs readiness. CI may start early and expand over time. M2 and M3 may advance in parallel after their dependencies. Do not let the flashy interface lane run ahead of unsolved identity, Unicode, or retention bugs.

### 14.2 Requirement-to-task traceability

| Requirement area | Responsible tasks |
|---|---|
| Data safety, undo, Unicode | 002–007, 011–013, 034, 048 |
| Schema, trash, TTL, ghost, pin | 005, 009–011, 028–032 |
| Search and note navigation | 018–027 |
| Command discoverability and keys | 014–015, 017, 023, 026 |
| Responsive UI, themes, mouse, motion | 016–017, 024, 033–038, 042 |
| Unix pipelines, CLI, export, clipboard | 008, 036, 039–041, 047 |
| CI, benchmarks, docs, compatibility | 001, 043–046 |

---

## 15. Session handoff, change control, and progress template

Maintain `docs/IMPLEMENTATION_PROGRESS.md` as the mutable operational state. The master spec defines intended behavior, while the progress log records actual evidence. If there is a contradiction: **stop and reconcile**, rather than assuming a task was done. Use the following schema (the bundle includes a ready-to-edit companion file).

```markdown
# Quicknote v2 — Implementation Progress
Spec: docs/QUICKNOTE_V2_SPEC.md, version 1.0.0
Repository HEAD: <SHA>
Last updated: <ISO datetime>
Active branch: <branch>
Current milestone: <M0..M6>
Current task: <QN-### or none>

## Task status
| ID | Status | Commit | Tests/notes |
| QN-001 | TODO | — | — |
| ... | ... | ... | ... |

## Last completed session
- Date and task:
- Scope performed:
- Files changed:
- Important design decisions:
- DB migration/version/backup status:
- Tests run exactly (pass/fail/not run):
- Known issues or blocked items:
- Next task and prerequisite rationale:

## Decision log
| Date | ID | Decision | Reason | Spec amendment? |

## Open defects and risks
| Issue | Severity | Reproduction | Owner / next action |

## Release gates
- [ ] P0 regressions
- [ ] Schema migration and restore
- [ ] P1 tasks done or approved deferrals
- [ ] UI keyboard/mouse/narrow tests
- [ ] CLI/shell tests
- [ ] Benchmarks
- [ ] Updated docs + release notes
```

### 15.1 Hand-off message format (copy verbatim between sessions)

```text
Quicknote v2 handoff — <date>
Repository: reeshadarian/quicknote-tui
Spec: docs/QUICKNOTE_V2_SPEC.md v<version>
HEAD: <sha>, branch: <name>, working tree: <clean/dirty>
DONE this session: QN-### — <what changed>
Tests PASSED: <exact commands, and counts if relevant>
Tests FAILED: <command + concise reason>
Tests NOT RUN: <command + reason>
Migrations: <none / version / backed up + verified>
Design decisions: <concise>
Open bugs/blockers: <concise>
Recommended next READY task: QN-### (<dependencies satisfied>)
Safety note: <any risk to old notes or destructive operation>
```

### 15.2 Scope and conflict policy

- If implementation uncovers new risks, create `BUG-XX` / `QN-049+` with rationale and test. Raise data-loss bugs to P0 and resolve them before cosmetic work.
- Any change to ghost/expiry/retention semantics, CLI stdout, schema, or keyboard defaults must be documented in the decision log and release notes.
- Always favor **preserving data** over auto-cleaning suspected duplicate records or silently discarding unsaved state.
- Never change all keybindings, packages, data schemas, and UI composition in one PR. Favor reversible steps and feature flags for high-risk areas.

---

## 16. First session: exact implementation instructions

**Recommended starting task: QN-001.** No attempt should be made to redesign the TUI before this gate.

1. Clone repo and pin baseline SHA. Run Go version, tests, vet, build. Record existing tests (there may be none) and failures truthfully.
2. Add test helper to create a temporary SQLite DB without touching `~/.local/share/quicknote-tui/notes.sqlite`; allow DB path injection as needed to enable safe tests.
3. Seed two uniquely identified persisted notes and create tests for BUG-01 search-open-save, BUG-02 duplicate trash recovery, BUG-03 undo isolation, BUG-04 non-ASCII backtick edit, and BUG-05 failed write. Initially failing regression tests are expected; mark those tests as `xfail`/skipped only while explicitly documenting each failure, never as silently passing.
4. Add a baseline CLI `-q` stdout test and a pure-render test. Capture actual behavior before fixing.
5. Update progress file with the reproducible failure list and mark QN-002 as next READY task (unless QN-001 reveals an even more urgent blocker).

**Example next-session request:**

> Implement QN-002 from `QUICKNOTE_V2_SPEC.md`. Read `IMPLEMENTATION_PROGRESS.md` first. Show the failing T-01/T-02 baseline, fix the active-note identity transition with the smallest possible change, run tests, and update the handoff. Do not work on themes, sidebar, or extra features yet.

---

## 17. Source references and caveats

**Code reviewed, public `main` branch on 2026-10-11:**

- [Repository overview / README](https://github.com/reeshadarian/quicknote-tui)
- [db.go](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/db.go)
- [tui.go](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/tui.go)
- [engine.go](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/engine.go)
- [main.go](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/main.go)
- [utils.go](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/utils.go)
- [go.mod](https://raw.githubusercontent.com/reeshadarian/quicknote-tui/main/go.mod)

This spec was built from source inspection and previously discussed product direction, **not a local clone, successful build, execution, or automated test**. The container in which this spec was prepared could not resolve GitHub for `git clone`; the public file contents were inspected through web access. A fresh implementation session must pin the exact Git SHA and verify facts against its working copy.

**Security note:** SQLite deletes (even with WAL checkpoints, `VACUUM`, or `secure_delete`) cannot promise secure erasure across SSD wear leveling, backups, swap, terminal history, and other storage layers. Quicknote must never claim that TTL or ghost conversion makes previously persisted secrets unrecoverable.

---

**End of Quicknote v2 implementation specification, v1.0.0.**