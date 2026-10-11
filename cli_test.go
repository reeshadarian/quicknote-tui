package main

import (
 "database/sql"
 "errors"
 "strings"
 "testing"
)

func TestQN008CLICreatesAndAppends(t *testing.T) {
 db:=newTestDB(t)
 if err:=persistCLIInput(db,"first","Project",false);err!=nil {t.Fatal(err)}
 var id int
 var content string
 if err:=db.QueryRow("SELECT id,content FROM notes").Scan(&id,&content);err!=nil {t.Fatal(err)}
 if content!="# Project\n\nfirst" {t.Fatalf("title creation: %q",content)}
 if err:=persistCLIInput(db,"second","Project",false);err!=nil {t.Fatal(err)}
 if got:=loadNoteContent(db,id);got!="# Project\n\nfirst\nsecond" {t.Fatalf("title append: %q",got)}
 if err:=persistCLIInput(db,"third","",true);err!=nil {t.Fatal(err)}
 if got:=loadNoteContent(db,id);got!="# Project\n\nfirst\nsecond\nthird" {t.Fatalf("last append: %q",got)}
 var count int
 if err:=db.QueryRow("SELECT count(*) FROM notes").Scan(&count);err!=nil {t.Fatal(err)}
 if count!=1 {t.Fatalf("created %d notes, want one",count)}
}

func TestQN008CLIErrorPropagation(t *testing.T) {
 db:=newTestDB(t)
 if _,err:=db.Exec("CREATE TRIGGER reject_cli BEFORE INSERT ON notes BEGIN SELECT RAISE(ABORT,'injected CLI failure'); END");err!=nil {t.Fatal(err)}
 err:=persistCLIInput(db,"data","",false)
 if err==nil || !strings.Contains(err.Error(),"injected CLI failure") {t.Fatalf("expected meaningful error, got %v",err)}
 var count int
 if err:=db.QueryRow("SELECT count(*) FROM notes").Scan(&count);err!=nil {t.Fatal(err)}
 if count!=0 {t.Fatalf("partial write: %d",count)}
}

func TestQN008CLIEmptyInput(t *testing.T) {
 db:=newTestDB(t)
 if err:=persistCLIInput(db,"","",false);err!=nil {t.Fatal(err)}
 var count int
 if err:=db.QueryRow("SELECT count(*) FROM notes").Scan(&count);err!=nil {t.Fatal(err)}
 if count!=0 {t.Fatalf("empty input created %d notes",count)}
}

func TestQN008CLIMissingRecentNoteCreatesNew(t *testing.T) {
 db:=newTestDB(t)
 if err:=persistCLIInput(db,"scratch","",true);err!=nil {t.Fatal(err)}
 var content string
 if err:=db.QueryRow("SELECT content FROM notes").Scan(&content);err!=nil {t.Fatal(err)}
 if content!="scratch" {t.Fatalf("content %q",content)}
}

func TestQN008CLIClosedDBReturnsError(t *testing.T) {
 db:=newTestDB(t)
 if err:=db.Close();err!=nil {t.Fatal(err)}
 err:=persistCLIInput(db,"scratch","",false)
 if err==nil || errors.Is(err,sql.ErrNoRows) {t.Fatalf("expected database failure, got %v",err)}
}
