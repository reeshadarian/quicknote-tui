package main

import (
 "strings"
 "testing"
)

func TestQN006TransactionRollsBackOnTagFailure(t *testing.T) {
 db:=newTestDB(t)
 original,err:=saveNoteChecked(db,Note{Content:"original #old"})
 if err!=nil {t.Fatal(err)}
 if _,err=db.Exec("CREATE TRIGGER reject_tags BEFORE INSERT ON tags BEGIN SELECT RAISE(ABORT, 'synthetic tag failure'); END");err!=nil {t.Fatal(err)}
 updated:=original
 updated.Content="changed #new"
 _,err=saveNoteChecked(db,updated)
 if err==nil || !strings.Contains(err.Error(),"synthetic tag failure") {t.Fatalf("expected injected failure, got %v",err)}
 if got:=loadNoteContent(db,original.ID);got!="original #old" {t.Fatalf("partial content update: %q",got)}
 var oldTag int
 if err=db.QueryRow("SELECT count(*) FROM tags WHERE note_id=? AND tag='#old'",original.ID).Scan(&oldTag);err!=nil {t.Fatal(err)}
 if oldTag!=1 {t.Fatalf("tags not restored on rollback: count=%d",oldTag)}
 var ft int
 if err=db.QueryRow("SELECT count(*) FROM notes_fts WHERE notes_fts MATCH 'changed'").Scan(&ft);err!=nil {t.Fatal(err)}
 if ft!=0 {t.Fatalf("rolled-back FTS content remains indexed: %d",ft)}
}

func TestQN006InsertFailureDoesNotCreatePartialNote(t *testing.T) {
 db:=newTestDB(t)
 if _,err:=db.Exec("CREATE TRIGGER reject_tags BEFORE INSERT ON tags BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END");err!=nil {t.Fatal(err)}
 note,err:=saveNoteChecked(db,Note{Content:"new #tag"})
 if err==nil {t.Fatal("injected tag failure did not stop insert")}
 if note.ID!=0 {t.Fatalf("failed insert allocated public note identity: %d",note.ID)}
 var count int
 if err=db.QueryRow("SELECT count(*) FROM notes").Scan(&count);err!=nil {t.Fatal(err)}
 if count!=0 {t.Fatalf("partial note persisted: %d",count)}
}

func TestQN006MissingOrTrashedIdentityCannotBeOverwritten(t *testing.T) {
 db:=newTestDB(t)
 id:=insertTestNote(t,db,"protected")
 trashNote(db,id)
 _,err:=saveNoteChecked(db,Note{ID:id,Content:"must not overwrite"})
 if err==nil {t.Fatal("saving a trashed note should fail")}
 if content:=loadNoteContent(db,id);content!="protected" {t.Fatalf("trashed note overwritten: %q",content)}
}
