package main

import (
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
			pipedText = fmt.Sprintf("Error executing command: %v\n\n%s", err, pipedText)
		}
		*titleFlag = *cmdFlag

	} else {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			bytes, _ := io.ReadAll(os.Stdin)
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
		var targetID int
		var existingContent string

		if *lastFlag {
			err := db.QueryRow("SELECT id, content FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1").Scan(&targetID, &existingContent)
			if err != nil {
				targetID = 0
			}
		} else if *titleFlag != "" {
			rows, _ := db.Query("SELECT id, content FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC")
			for rows.Next() {
				var id int
				var content string
				rows.Scan(&id, &content)

				if strings.EqualFold(extractTitle(content), sanitizeFilename(*titleFlag)) {
					targetID = id
					existingContent = content
					break
				}
			}
			rows.Close()

			if targetID == 0 {
				initialContent = "# " + *titleFlag + "\n\n" + initialContent
			}
		}

		if targetID > 0 {
			newContent := strings.TrimRight(existingContent, " \n") + "\n" + initialContent
			_, err := db.Exec("UPDATE notes SET content = ?, updated_at = datetime('now', '+1 second') WHERE id = ?", newContent, targetID)
			if err != nil {
				log.Fatalf("Failed to append to CLI note: %v", err)
			}
		} else {
			_, err := db.Exec("INSERT INTO notes (content, updated_at) VALUES (?, datetime('now', '+1 second'))", initialContent)
			if err != nil {
				log.Fatalf("Failed to save CLI note: %v", err)
			}
		}

		if *quietFlag {
			fmt.Println("✓ Note saved to Quicknote.")
			return
		}
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