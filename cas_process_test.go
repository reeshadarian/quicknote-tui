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

// Child test binaries model separate Quicknote processes, each holding the
// same base revision before the parent releases both writes.
func TestQN011ProcessWriter(t *testing.T) {
	if os.Getenv("QN_CAS_CHILD") != "1" {
		return
	}
	db, err := sql.Open("sqlite3", os.Getenv("QN_CAS_DB")+"?_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		os.Exit(3)
	}
	store := NewSQLiteStore(db)
	id, _ := strconv.Atoi(os.Getenv("QN_CAS_ID"))
	n, err := store.GetNote(id)
	if err != nil {
		os.Exit(3)
	}
	fmt.Println("ready")
	if _, err = bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		os.Exit(3)
	}
	n.Content = os.Getenv("QN_CAS_TEXT")
	_, err = store.Save(n)
	db.Close()
	if errors.Is(err, ErrConflict) {
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	os.Exit(0)
}

func TestQN011T17IndependentProcesses(t *testing.T) {
	a, _ := twoStores(t)
	n, err := a.Save(Note{Content: "process base"})
	if err != nil {
		t.Fatal(err)
	}
	var path string
	if err := a.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmds := make([]*exec.Cmd, 2)
	start := make([]func(), 2)
	for i := range cmds {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQN011ProcessWriter$")
		cmd.Env = append(os.Environ(), "QN_CAS_CHILD=1", "QN_CAS_DB="+path, "QN_CAS_ID="+strconv.Itoa(n.ID), fmt.Sprintf("QN_CAS_TEXT=process %d", i))
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = cmd
		t.Cleanup(func() {
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		})
		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil || line != "ready\n" {
			t.Fatalf("child not ready: %q %v", line, err)
		}
		start[i] = func() { fmt.Fprintln(in, "save"); in.Close() }
	}
	for _, release := range start {
		release()
	}
	success, conflict := 0, 0
	for _, cmd := range cmds {
		err := cmd.Wait()
		if err == nil {
			success++
		} else {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 2 {
				conflict++
			} else {
				t.Fatalf("child failure: %v", err)
			}
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	current, err := a.GetNote(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != n.Revision+1 || (current.Content != "process 0" && current.Content != "process 1") {
		t.Fatalf("invalid winner: %+v", current)
	}
}
