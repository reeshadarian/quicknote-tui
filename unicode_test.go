package main

import (
 "testing"
 tea "github.com/charmbracelet/bubbletea"
)

func TestQN004GraphemeBoundaries(t *testing.T) {
 for _,tc:=range []struct{name,text string; at,wantPrevious,wantNext int}{
  {"ascii","abc",2,1,3},
  {"accent","e\u0301x",2,0,3},
  {"within accent","e\u0301x",1,0,2},
  {"ZWJ emoji","👩‍💻!",3,0,4},
  {"flag","🇺🇸x",2,0,3},
  {"CJK","東京",1,0,2},
  {"empty","",0,0,0},
 } {
  t.Run(tc.name,func(t *testing.T) {
   r:=[]rune(tc.text)
   if got:=previousGraphemeBoundary(r,tc.at);got!=tc.wantPrevious {t.Errorf("previous=%d want %d",got,tc.wantPrevious)}
   if got:=nextGraphemeBoundary(r,tc.at);got!=tc.wantNext {t.Errorf("next=%d want %d",got,tc.wantNext)}
  })
 }
}

func TestQN004BacktickAfterMultibyteDoesNotPanic(t *testing.T) {
 db:=newTestDB(t)
 id:=insertTestNote(t,db,"é")
 m:=initialModel(db,nowForTest())
 m.notes=[]Note{{ID:id,Content:"é"}}
 m.index=0
 m.buffer=stringToBuffer("é")
 m.cursorCol=1
 next,_:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'\x60'}})
 got:=next.(model)
 if gotText:=bufferToString(got.buffer);gotText!="é\x60\x60" {t.Fatalf("backtick insertion = %q",gotText)}
}

func TestQN004BackspaceRemovesEntireCombiningSequence(t *testing.T) {
 db:=newTestDB(t)
 id:=insertTestNote(t,db,"e\u0301!")
 m:=initialModel(db,nowForTest())
 m.notes=[]Note{{ID:id,Content:"e\u0301!"}}
 m.buffer=stringToBuffer("e\u0301!")
 m.cursorCol=2
 next,_:=m.Update(tea.KeyMsg{Type:tea.KeyBackspace})
 got:=next.(model)
 if text:=bufferToString(got.buffer);text!="!" {t.Fatalf("backspace left %q",text)}
 if got.cursorCol!=0 {t.Fatalf("cursor=%d want 0",got.cursorCol)}
}
