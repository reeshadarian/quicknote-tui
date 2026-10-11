package main

import (
	"flag"
	"fmt"
	"io"
	"errors"
	"database/sql"
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
		if statErr != nil { log.Fatalf("inspect stdin: %v", statErr) }
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			bytes, readErr := io.ReadAll(os.Stdin)
			if readErr != nil { log.Fatalf("read stdin: %v", readErr) }
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
	if *quietFlag { return }

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
// persistCLIInput uses one transaction for the note operation. All failures
// propagate to main so shell callers receive a nonzero exit status.
func persistCLIInput(db *sql.DB, content, title string, last bool) error {
 if content=="" { return nil }
 tx,err:=db.Begin()
 if err!=nil {return fmt.Errorf("begin CLI save: %w",err)}
 defer tx.Rollback()
 var id int
 var existing string
 if last {
  err=tx.QueryRow("SELECT id, content FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1").Scan(&id,&existing)
  if err!=nil && !errors.Is(err,sql.ErrNoRows) {return fmt.Errorf("lookup recent note: %w",err)}
 } else if title!="" {
  rows,e:=tx.Query("SELECT id, content FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC")
  if e!=nil {return fmt.Errorf("lookup titled note: %w",e)}
  for rows.Next() {
   var candidateID int
   var candidateContent string
   if e=rows.Scan(&candidateID,&candidateContent);e!=nil {break}
   if strings.EqualFold(extractTitle(candidateContent),sanitizeFilename(title)) {
    id,existing=candidateID,candidateContent
    break
   }
  }
  if e==nil {e=rows.Err()}
  closeErr:=rows.Close()
  if e!=nil {return fmt.Errorf("scan titled notes: %w",e)}
  if closeErr!=nil {return fmt.Errorf("close title query: %w",closeErr)}
  if id==0 {content="# "+title+"\n\n"+content}
 }
 if id>0 {
  res,e:=tx.Exec("UPDATE notes SET content = ?, updated_at = datetime('now', '+1 second') WHERE id = ? AND deleted_at IS NULL",strings.TrimRight(existing," \n")+"\n"+content,id)
  if e!=nil {return fmt.Errorf("append to note %d: %w",id,e)}
  affected,e:=res.RowsAffected()
  if e!=nil {return fmt.Errorf("check append to note %d: %w",id,e)}
  if affected!=1 {return fmt.Errorf("note %d no longer available",id)}
 } else {
  if _,err=tx.Exec("INSERT INTO notes (content, updated_at) VALUES (?, datetime('now', '+1 second'))",content);err!=nil {
   return fmt.Errorf("create note: %w",err)
  }
 }
 if err=tx.Commit();err!=nil {return fmt.Errorf("commit CLI save: %w",err)}
 return nil
}
