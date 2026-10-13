# Quicknote v2 — session handoff

The approved M2 lifecycle milestone (QN-028–032) is complete. Continue with **QN-018: Filter/query grammar** after inspecting actual current state; QN-009 satisfies its dependency. The living ledger contains the full historical evidence and exact commits; the archived template is not authoritative.

Read AGENTS.md, the entire docs/QUICKNOTE_V2_SPEC.md, docs/IMPLEMENTATION_PROGRESS.md and docs/LOCAL_TESTING.md before editing. Use the actual GitButler branch/code rather than old session text. Specification 1.1.0, SQLite schema 3, Go 1.25.9+ with CGO/GCC and sqlite_fts5. Stable main baseline is d3e5a119cdf12ec99fcc14a62545f60729adea54 and remains protected.

## Accepted behavior and architectural contracts

- Absolute TTL survives save/restart; source precedence distinguishes directive/preset/none. Unchanged observed directives do not rearm after Never, pin/unpin or no-timer restore. Invalid/malformed deadlines require review and cannot automatically expire. Custom local dates reject DST ambiguity. Mutation confirmations use current/injected action clock; View uses captured display time.
- Verified unique SQLite VACUUM INTO backups precede transactional schema upgrades. Historical timestamp bytes and IDs are preserved. Mixed legacy/new list and CLI ordering uses julianday(timestamp), then ID, avoiding wrong-note append. Foreign keys are enforced per connection, and orphan legacy tags block migration without deleting notes.
- Maintenance protects owned active/dirty/failed/saving drafts, saves latest content before expiry, and strictly purges only after deleted+72h (exact boundary is recoverable). Explicit queued user actions take priority over automatic owned expiry. Superseded acknowledgments settle dependent queued work visibly with reopen/retry; no orphan queue or silent success.
- Keep CAS content/revision pairing: superseded full loads refetch complete notes; never promote an old buffer to a newer summary revision. Preserve per-note histories, local versions, immutable request generations, matching owner navigation/exit, late successful writes and dirty recovery.
- Ghosts allocate session identity before edits; no automatic SQLite/FTS/log/file/clipboard persistence. Safe conversion keeps the original by default or explicitly moves it to 72h Trash. Default Cancel guards persistence/copy/export; explicit output can leave memory-only mode. Discard removes owned references and invalidates stale output checks, without promising OS/terminal/backup erasure.
- Trash is read-only, with reason/deletion/recovery metadata and scrolling clamped to the last full viewport. Restore defaults Cancel, then no timer; reset is offered only for validated remembered directive timers and respects pins. Ordinary persisted empty notes soft-delete; ID-zero/ghost discard never creates Trash.
- Latest manual Undo stores only ID/revision/recovery bound, including ghostTrash and late acknowledgments; existing request generations order intents across clock corrections. Ctrl+Z remains text undo. Permanent deletion is explicit/default Cancel, Trash-only state/revision CAS with atomic selected notes/tags/FTS rollback. SourcePurged is explicit ownership presence, including imported revision 0; preserve newer dirty/version/revision recovery during follow-up load/refetch.

## Shared code seams

lifecycle.go defines Clock, pure directive/expiration rules, timestamp parsing and lifecycle values. migration_lifecycle.go owns v3 migration/verified backup; store_lifecycle.go owns lifecycle CAS/maintenance; openDatabase(path,Clock) returns startup errors. LifecycleStore is embedded in NoteStore; internal compatibility adapters remain.

tui_lifecycle.go owns the existing request/queue/maintenance/ack controller. ttl_picker.go supplies resolver/picker and current action-clock seam. ghost.go supplies memory creation/conversion/common discard. trash_ui.go supplies read-only rendering/restore/Undo/forever. layout.go supplies shared panel body geometry; overlays/keymap/actions keep keyboard routes and default-Cancel confirmation. Do not add a parallel save queue or attach summary revisions to stale full content.

## Version control and safe workspace use

All version-control inspection and writes use GitButler. Read /home/rarian/.codex/skills/gitbutler/SKILL.md first. Installed CLI cannot operate linked worktrees; retain existing worker branches/worktrees. Use one product writer in the primary checkout and a dedicated branch anchored above the current accepted dev/v2:

```zsh
but status
but show dev/v2
but branch new qn-v2/QN-018-query --above dev/v2
# After implementation/tests, inspect EACH owned path; never use a workspace-wide diff.
but diff <owned-path> --json
but commit -b qn-v2/QN-018-query -m 'QN-018: implement validated query grammar' <owned-ids>
```

Coordinator integration (only authorized reviewed commits, oldest first):

```zsh
but setup
but teardown --checkout-to dev/v2
but pick <owned-source-commit> --branch dev/v2
```

The GitButler target metadata is still origin/main. Never blindly land/pull against it. The tested setup/explicit teardown/pick route preserves source refs and does not rewrite prior dev history. Use individual owned-path diffs to avoid accidental config/log/database content inspection. Pre-existing .config/quicknote-tui/theme.json, .local/share/quicknote-tui/notes.sqlite and vim_profile.log stay uncommitted and unchanged by our actions. One historical workspace-wide diff accidentally included config/log content; no user files were modified/committed or production SQLite queries/tests performed.

Earlier GitButler push unexpectedly created origin/qn-v2/QN-028-durable-ttl at 869a7d0e17c50d94b60f72dcdcae57bc421189af despite a dev-only dry run. It contains existing dev commits and remains documented. Installed CLI has no supported safe remote-ref deletion/exclusion path; no raw Git/API or main-target land workaround was used. Subsequent actual pushes reported only dev/v2. Future publication requires the user's task authorization; current M2 dev publication was explicit. Never alter main, open a PR or rewrite shared/reviewed history without authorization.

## Verified implementation and testing

Final tested/published implementation: **f4b501a96183773540898672a1d28198b6f74a2d**. Full Go tests (23.922s), race (92.960s), vet and normal build passed on byte-identical code (59 source hashes checked across dev integration). Final broad 20× regression run (279.237s), directive/Unicode fuzz and corrected-dev CLI 10/10 and PTY 15/15 passed. One broad milestone review and its single correction/re-review closed all four findings; no unresolved blocking or minor review issue. The living ledger lists all task/harness/source/copy SHAs, exact commands, red/green history and publication deviation.

## Testing and limits

Use only disposable synthetic HOMEs/databases. Run the Go full/race/vet/normal-build gates with sqlite_fts5 and meaningful selected repetitions; docs/LOCAL_TESTING.md has current commands. Reproducible Python stdlib harnesses are tests/m2_cli.py and tests/m2_pty.py; full runs omit --baseline and require CLI 10/10, PTY 15/15 with zero FAIL/MISSING. They isolate clipboard backends, use bounded timeouts and real SQLite contention, and verify note/tag/FTS identity/revisions without printing payloads. Exact72h/DST use injected model/store clocks, not system clock changes or multi-day waits.

Human Fedora/Zsh/Kitty/tmux and real clipboard, remote CI and release benchmarks remain NOT RUN. Scripted PTY does not substitute for human platform testing. No production note database was migrated/backed up or used for deletion tests. Pipeline trailing-newline behavior/general export/clipboard redesign and measured performance optimization remain their later roadmap tasks. No secure-erasure or latency guarantee is made.

## Ready-to-paste continuation

> Continue Quicknote v2 on dev/v2. Read AGENTS.md, the full specification, living progress ledger, this handoff and LOCAL_TESTING.md; inspect current But branch/source. M2 QN-028–032 and QN-048 are complete with reviewed commits and synthetic Go/race/fuzz/CLI/PTY evidence. Select QN-018 (dependency 009 done) unless new P0 evidence or branch changes take priority. Preserve CAS content/revision pairing, source generations/queues, stable IDs/undo/dirty state, schema 3 backups/legacy data, durable TTL and 72h Trash, ghost privacy and keyboard routes. Use only synthetic fixtures, commit only owned changes via GitButler, update exact SHA/test/failure/NOTRUN evidence and protect main. No unrelated rewrites.
