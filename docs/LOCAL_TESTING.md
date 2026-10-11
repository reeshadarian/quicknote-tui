# Local testing on Fedora

Use Go 1.25.9 or newer. This checklist avoids using your personal notes.

```bash
git fetch origin
git switch dev/v2
git pull --ff-only origin dev/v2
go version
go mod download
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/quicknote-v2-smoke .
```

For the new store and migration tests:

```bash
go test -count=1 -v -run 'TestQN009|TestQN010' .
```

Use an isolated HOME for manual checks:

```bash
tmp_home="$(mktemp -d)"
HOME="$tmp_home" /tmp/quicknote-v2-smoke -n "test note" -q
HOME="$tmp_home" /tmp/quicknote-v2-smoke
```

Do not run a development binary against real notes until the migration tests pass. The migration should create a pre-v2 backup of an existing v1 database. The v2 schema adds revision, expires_at, and is_pinned. CAS revision enforcement and TTL scheduling are future tasks.

Provide test command output and git commit SHA in your next implementation session.
