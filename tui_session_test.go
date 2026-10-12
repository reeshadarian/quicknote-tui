package main

import (
 "testing"
 "strings"
 tea "github.com/charmbracelet/bubbletea"
)

// T-03: edit A, visit B, then return to A. History, cursor and viewport
// must remain attached to their own note identities.
func TestQN003NoteSessionsRemainIsolated(t *testing.T) {
 db:=newTestDB(t)
 a:=insertTestNote(t,db,"alpha")
 b:=insertTestNote(t,db,"beta")
 m:=initialModel(db,nowForTest())
 m.notes=[]Note{{ID:a,Content:"alpha"},{ID:b,Content:"beta"}}
 m.index=0
 m.buffer=stringToBuffer("alpha revised")
 m.needsSave=true
 m.cursorRow=0
 m.cursorCol=5
 *m.viewportTop=4
 m.undoStack=[]textState{{content:"alpha",row:0,col:3}}
 next,_:=m.Update(tea.KeyMsg{Type:tea.KeyRight,Alt:true}) // navigation alt+right
 m=next.(model)
 if m.notes[m.index].ID!=b { t.Fatalf("navigation opened ID %d, want %d",m.notes[m.index].ID,b) }
 if len(m.undoStack)!=0 {t.Fatal("A undo history leaked into B")}
 if m.needsSave {t.Fatal("A dirty flag leaked into B")}
 m.buffer=stringToBuffer("beta revised")
 m.needsSave=true
 m.cursorCol=2
 m.undoStack=[]textState{{content:"beta"}}
 next,_=m.Update(tea.KeyMsg{Type:tea.KeyLeft,Alt:true}) // navigation alt+left
 m=next.(model)
 if m.notes[m.index].ID!=a {t.Fatal("did not return to A")}
 if got:=bufferToString(m.buffer);strings.TrimSuffix(got,"\n")!="alpha revised" {t.Fatalf("A draft lost: %q",got)}
 if m.cursorCol!=5 {t.Fatalf("A cursor lost: %d",m.cursorCol)}
 if *m.viewportTop!=4 {t.Fatalf("A viewport lost: %d",*m.viewportTop)}
 if len(m.undoStack)!=1 || m.undoStack[0].content!="alpha" {t.Fatalf("A history lost: %+v",m.undoStack)}
 if m.needsSave {t.Fatal("A should have been saved before navigating")}
 if got:=loadNoteContent(db,a);strings.TrimSuffix(got,"\n")!="alpha revised" {t.Fatalf("A not saved: %q",got)}
 next,_=m.Update(tea.KeyMsg{Type:tea.KeyRight,Alt:true})
 m=next.(model)
 if got:=bufferToString(m.buffer);strings.TrimSuffix(got,"\n")!="beta revised" {t.Fatalf("B draft lost: %q",got)}
 if len(m.undoStack)!=1 || m.undoStack[0].content!="beta" {t.Fatalf("B history lost: %+v",m.undoStack)}
 if m.cursorCol!=2 {t.Fatalf("B cursor lost: %d",m.cursorCol)}
}

// Preserve draft state in memory without visiting production storage.
func TestQN003DirectSessionSnapshot(t *testing.T) {
 db:=newTestDB(t)
 a:=insertTestNote(t,db,"a")
 b:=insertTestNote(t,db,"b")
 m:=initialModel(db,nowForTest())
 m.notes=[]Note{{ID:a,Content:"a"},{ID:b,Content:"b"}}
 m.index=0
 m.buffer=stringToBuffer("draft a")
 m.needsSave=true
 m.undoStack=[]textState{{content:"a"}}
 m.stashNote()
 m.index=1
 m.activateNote()
 if m.needsSave || len(m.undoStack)>0 {t.Fatal("stale state from A in B")}
 m.stashNote()
 m.index=0
 m.activateNote()
 if !m.needsSave || strings.TrimSuffix(bufferToString(m.buffer),"\n")!="draft a" {t.Fatal("A dirty draft not restored")}
 if len(m.undoStack)!=1 || m.undoStack[0].content!="a" {t.Fatal("A history not restored")}
}
