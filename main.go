package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	startTime := time.Now()

	noteFlag := flag.String("n", "", "Create a note directly from the command line")
	quietFlag := flag.Bool("q", false, "Quiet mode: save note and exit without opening the UI")
	titleFlag := flag.String("t", "", "Title/Heading for the note (creates new or appends to existing)")
	lastFlag := flag.Bool("l", false, "Append to the most recently used note")
	cmdFlag := flag.String("c", "", "Execute a shell command, capture output, and use command as title")

	flag.Parse()

	var pipedText string
	var tuiInput *os.File = os.Stdin

	if *cmdFlag != "" {
		cmd := exec.Command("sh", "-c", *cmdFlag)
		out, err := cmd.CombinedOutput()

		pipedText = strings.TrimSpace(string(out))
		if err != nil {
			log.Fatalf("command %q failed: %v: %s", *cmdFlag, err, pipedText)
		}
		*titleFlag = *cmdFlag

	} else {
		stat, statErr := os.Stdin.Stat()
		if statErr != nil {
			log.Fatalf("inspect stdin: %v", statErr)
		}
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			bytes, readErr := io.ReadAll(os.Stdin)
			if readErr != nil {
				log.Fatalf("read stdin: %v", readErr)
			}
			pipedText = strings.TrimSpace(string(bytes))

			tty, err := os.Open("/dev/tty")
			if err == nil {
				tuiInput = tty
			}
		}
	}

	db := initDB()
	defer db.Close()

	initialContent := ""
	if *noteFlag != "" {
		initialContent = *noteFlag
	} else if pipedText != "" {
		initialContent = pipedText
	}

	if initialContent != "" {
		if err := persistCLIInput(db, initialContent, *titleFlag, *lastFlag); err != nil {
			log.Fatalf("save CLI note: %v", err)
		}
	}
	// Quiet capture is silent on success, including when there is no input.
	if *quietFlag {
		return
	}

	p := tea.NewProgram(
		initialModel(db, startTime),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithInput(tuiInput),
	)
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// persistCLIInput selects a snapshot, then uses the same transactional CAS save
// as the editor. A concurrent append fails visibly rather than dropping text.
func persistCLIInput(db *sql.DB, content, title string, last bool) error {
	return persistCLIInputWithStore(db, NewSQLiteStore(db), content, title, last)
}

// Injection seam for deterministic concurrent CLI regression tests.
func persistCLIInputWithStore(db *sql.DB, store NoteStore, content, title string, last bool) error {
	if content == "" {
		return nil
	}
	var id int
	if last {
		err := db.QueryRow("SELECT id FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC, id DESC LIMIT 1").Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup recent note: %w", err)
		}
	} else if title != "" {
		rows, err := db.Query("SELECT id,content FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC, id DESC")
		if err != nil {
			return fmt.Errorf("lookup titled note: %w", err)
		}
		for rows.Next() {
			var candidateID int
			var candidateContent string
			if err = rows.Scan(&candidateID, &candidateContent); err != nil {
				break
			}
			if strings.EqualFold(extractTitle(candidateContent), sanitizeFilename(title)) {
				id = candidateID
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	n := Note{Content: content}
	if id > 0 {
		var err error
		n, err = store.GetNote(id)
		if err != nil {
			return err
		}
		n.Content = strings.TrimRight(n.Content, " \n") + "\n" + content
	} else if title != "" && !last {
		n.Content = "# " + title + "\n\n" + content
	}
	_, err := store.Save(n)
	return err
}
