# QN-001 — baseline test report

**Branch:** `dev/v2`
**Baseline main commit:** `d3e5a119cdf12ec99fcc14a62545f60729adea54` (observed from GitHub code-search file links).
**Go requirement:** `go 1.25.9` in `go.mod`.
**Status:** TESTS ADDED; EXECUTION UNVERIFIED.

## Synthetic fixture safety

`newTestDB(t)` creates `t.TempDir()`, overrides `HOME` with `t.Setenv`, then calls the application's real `initDB()`. It asserts that SQLite's main database path is the expected *temporary* notes.sqlite. Tests using this fixture must not use `t.Parallel()`, because HOME is process-global. Production databases are not accessed.

## Tests implemented

- `TestSyntheticDatabaseIsolationAndFTS`: fixture isolation, v1 schema, FTS trigger, note retrieval.
- `TestSyntheticNoteSaveAndOrdinaryTrash`: insert/save, tag-bearing content, ordinary soft delete, restore.
- `TestEditorBufferUnicodeRoundTrip`: roundtrip smoke cases including CJK, emoji, combining mark.
- `TestRegressionSearchSelectionMaintainsNoteIdentity`: intentionally skipped pending QN-002 / T-01; failing assertion retained.
- `TestRegressionDuplicateTrashPreservesBothNotes`: intentionally skipped pending QN-005 / T-05; failing assertion retained.

**Important:** Skipped regressions are *not* passing functionality; unskip after their associated fixes. More realistic editor event tests remain necessary in QN-004.

## Runbook

```bash
go version
go test ./...
go vet ./...
go build ./...
go test -race ./...
```

No local code execution has been verified from this session: container networking cannot clone GitHub, and CI results have not yet been observed. Do not report these checks as passing unless logs verify them.

## Known pre-existing defects (unfixed)

1. Search result Enter changes editor buffer without updating selected note identity.
2. `trashNote` hard-deletes duplicate-content notes.
3. `initDB` deduplicates the trash destructively.
4. Several SQLite errors are ignored.

## Next task

QN-002: fix selected note identity, enable the search regression test, then run the baseline suite in CI/local environment.
