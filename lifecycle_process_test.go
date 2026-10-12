package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestQN028LifecycleProcessWorker(t *testing.T) {
	if os.Getenv("QN_LIFECYCLE_CHILD") != "1" {
		return
	}
	db, e := sql.Open("sqlite3", os.Getenv("QN_LIFECYCLE_DB")+"?_busy_timeout=5000&_txlock=immediate&_foreign_keys=on")
	if e != nil {
		os.Exit(3)
	}
	defer db.Close()
	id, _ := strconv.Atoi(os.Getenv("QN_LIFECYCLE_ID"))
	s := NewSQLiteStoreWithClock(db, fixedClock{lifecycleNow()})
	n, e := s.GetNote(id)
	if e != nil {
		os.Exit(3)
	}
	fmt.Println("ready")
	if _, e = bufio.NewReader(os.Stdin).ReadString('\n'); e != nil {
		os.Exit(3)
	}
	now := lifecycleNow().Add(time.Minute)
	switch os.Getenv("QN_LIFECYCLE_OP") {
	case "sweep":
		_, e = s.Maintain(now, nil)
	case "content":
		n.Content = "winning content #kill 2 hours"
		_, e = s.Save(n)
	case "pin":
		_, e = s.ApplyPinnedCAS(id, n.Revision, true, now)
	case "ttl":
		at := now.Add(time.Hour)
		_, e = s.ApplyExpirationCAS(id, n.Revision, ExpirationChoice{Deadline: &at, Source: ExpirationPreset}, now)
	case "expire":
		_, e = s.ExpireNoteCAS(id, n.Revision, now)
	}
	if errors.Is(e, ErrConflict) || errors.Is(e, ErrTrashed) || errors.Is(e, ErrWrongState) {
		os.Exit(2)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(3)
	}
	os.Exit(0)
}
func TestQN028LifecycleCASRace(t *testing.T) {
	for _, op := range []string{"content", "pin", "ttl", "sweep"} {
		t.Run(op, func(t *testing.T) {
			db := newTestDB(t)
			s := NewSQLiteStoreWithClock(db, fixedClock{lifecycleNow()})
			n, e := s.Save(Note{Content: "#kill 1 minute"})
			if e != nil {
				t.Fatal(e)
			}
			var path string
			db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			operations := []string{"expire", op}
			if op == "sweep" {
				operations[0] = "sweep"
			}
			var commands []*exec.Cmd
			var release []func()
			for _, operation := range operations {
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQN028LifecycleProcessWorker$")
				cmd.Env = append(os.Environ(), "QN_LIFECYCLE_CHILD=1", "QN_LIFECYCLE_DB="+path, "QN_LIFECYCLE_ID="+strconv.Itoa(n.ID), "QN_LIFECYCLE_OP="+operation)
				out, e := cmd.StdoutPipe()
				if e != nil {
					t.Fatal(e)
				}
				in, e := cmd.StdinPipe()
				if e != nil {
					t.Fatal(e)
				}
				cmd.Stderr = os.Stderr
				if e = cmd.Start(); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() {
					if cmd.Process != nil {
						cmd.Process.Kill()
					}
				})
				line, e := bufio.NewReader(out).ReadString('\n')
				if e != nil || line != "ready\n" {
					t.Fatal(line, e)
				}
				commands = append(commands, cmd)
				release = append(release, func() { fmt.Fprintln(in, "go"); in.Close() })
			}
			for _, goWrite := range release {
				goWrite()
			}
			success, conflict := 0, 0
			for _, cmd := range commands {
				if e := cmd.Wait(); e == nil {
					success++
				} else {
					var exit *exec.ExitError
					if errors.As(e, &exit) && exit.ExitCode() == 2 {
						conflict++
					} else {
						t.Fatal(e)
					}
				}
			}
			if op != "sweep" && (success != 1 || conflict != 1) {
				t.Fatal(success, conflict)
			}
			got, e := s.GetNote(n.ID)
			if e != nil || got.Revision != 2 {
				t.Fatal(got, e)
			}
			if got.DeletedAt != "" {
				if got.Content != n.Content || got.DeleteReason != DeleteExpired {
					t.Fatal(got)
				}
			} else {
				switch op {
				case "content":
					if got.Content != "winning content #kill 2 hours" {
						t.Fatal(got)
					}
				case "pin":
					if !got.IsPinned || got.ExpiresAt.Valid {
						t.Fatal(got)
					}
				case "ttl":
					if got.ExpirationSource != ExpirationPreset {
						t.Fatal(got)
					}
				}
			}
		})
	}
}
