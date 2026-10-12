# Local testing on Fedora

Use Go 1.25.9 or newer. This checklist avoids using your personal notes.

```bash
git fetch origin
git switch dev/v2
git pull --ff-only origin dev/v2
go version
go mod download
export CGO_ENABLED=1
export GOFLAGS='-tags=sqlite_fts5'
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/quicknote-v2-smoke .
```

For the new store and migration tests:

```bash
go test -count=1 -v -run 'TestQN009|TestQN010|TestQN011' .
```

Use an isolated HOME for manual checks:

```bash
tmp_home="$(mktemp -d)"
HOME="$tmp_home" /tmp/quicknote-v2-smoke -n "test note" -q
HOME="$tmp_home" /tmp/quicknote-v2-smoke
```

Do not run a development binary against real notes until the migration tests pass. The migration should create a pre-v2 backup of an existing v1 database. The v2 schema adds revision, expires_at, and is_pinned. CAS revision enforcement is implemented. Async I/O and TTL scheduling remain future tasks. QN-011 includes separate-connection and separate-process concurrency regressions and keyboard conflict recovery; see the living ledger for verification details.

Provide test command output and git commit SHA in your next implementation session.

Quicknote uses SQLite FTS5. The `go-sqlite3` driver requires `sqlite_fts5` at build time; use the same tag for all tests and release builds. On Fedora, ensure `gcc` is installed. Avoid `-tags libsqlite3` unless intentionally using a verified system SQLite with FTS5.
