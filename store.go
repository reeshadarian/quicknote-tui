package main

import (
 "database/sql"
 "fmt"
)

// NoteStore separates application navigation from SQLite. The checked methods
// return errors; legacy UI adapters are transitional until QN-013.
type NoteStore interface {
 Get(int) (string,error)
 List(bool) []Note
 Search(string,bool) []Note
 Save(Note) (Note,error)
 Trash(int) error
 Restore(int) error
 Delete(int) error
 Purge(int) error
}

type SQLiteStore struct { db *sql.DB }
func NewSQLiteStore(db *sql.DB) *SQLiteStore {return &SQLiteStore{db:db}}
func (s *SQLiteStore) Get(id int) (string,error) {
 if id<=0 {return "",fmt.Errorf("invalid note id %d",id)}
 var v string
 err:=s.db.QueryRow("SELECT content FROM notes WHERE id=?",id).Scan(&v)
 return v,err
}
func (s *SQLiteStore) List(trash bool) []Note {return loadNotes(s.db,trash)}
func (s *SQLiteStore) Search(q string,trash bool) []Note {return searchNotes(s.db,q,trash)}
func (s *SQLiteStore) Save(n Note) (Note,error) {return saveNoteChecked(s.db,n)}
func (s *SQLiteStore) Trash(id int) error {
 if id<=0{return fmt.Errorf("invalid note id %d",id)}
 return execNoteMutation(s.db,"UPDATE notes SET deleted_at=COALESCE(deleted_at,CURRENT_TIMESTAMP) WHERE id=? AND deleted_at IS NULL",id)
}
func (s *SQLiteStore) Restore(id int) error {
 if id<=0{return fmt.Errorf("invalid note id %d",id)}
 return execNoteMutation(s.db,"UPDATE notes SET deleted_at=NULL, updated_at=CURRENT_TIMESTAMP, kill_start=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NOT NULL",id)
}
func (s *SQLiteStore) Delete(id int) error {return s.Purge(id)}
func (s *SQLiteStore) Purge(id int) error {
 if id<=0{return fmt.Errorf("invalid note id %d",id)}
 tx,err:=s.db.Begin()
 if err!=nil{return err}
 defer tx.Rollback()
 if _,err=tx.Exec("DELETE FROM tags WHERE note_id=?",id);err!=nil{return err}
 res,err:=tx.Exec("DELETE FROM notes WHERE id=?",id)
 if err!=nil{return err}
 affected,err:=res.RowsAffected()
 if err!=nil{return err}
 if affected!=1{return fmt.Errorf("note %d not found",id)}
 return tx.Commit()
}
func execNoteMutation(db *sql.DB,query string,id int) error {
 res,err:=db.Exec(query,id)
 if err!=nil{return err}
 n,err:=res.RowsAffected()
 if err!=nil{return err}
 if n!=1{return fmt.Errorf("note %d not found or wrong state",id)}
 return nil
}
