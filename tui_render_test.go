package main

import (
 "testing"
 "time"
)

func TestQN007RenderingDoesNotResetKillTimer(t *testing.T) {
 db:=newTestDB(t)
 m:=initialModel(db,time.Date(2026,10,11,12,0,0,0,time.UTC))
 m.notes=[]Note{{ID:1, Content:"#kill 1 hour",KillStart:time.Date(2026,10,11,11,30,0,0,time.UTC)}}
 m.index=0
 m.buffer=stringToBuffer("#kill 1 hour")
 m.viewport.Width=100
 m.viewport.Height=20
 m.timerNow=time.Date(2026,10,11,12,0,0,0,time.UTC)
 original:=m.notes[0].KillStart
 first:=m.View()
 second:=m.View()
 if first!=second {t.Fatal("render output changed without model update")}
 if !m.notes[0].KillStart.Equal(original) {t.Fatal("render reset note expiration timestamp")}
}

func TestQN007RenderingDoesNotMutateScroll(t *testing.T) {
 db:=newTestDB(t)
 m:=initialModel(db,time.Now())
 m.notes=[]Note{{ID:0,Content:"a\nb\nc\nd\ne"}}
 m.buffer=stringToBuffer("a\nb\nc\nd\ne")
 m.cursorRow=4
 m.viewport.Width=80
 m.viewport.Height=2
 *m.viewportTop=0
 _=m.View()
 if *m.viewportTop!=0 {t.Fatalf("View changed shared viewportTop to %d",*m.viewportTop)}
 _=m.renderEditorView()
 if *m.viewportTop!=0 {t.Fatal("renderEditorView mutated viewport")}
}

func TestQN007TickClockUpdate(t *testing.T) {
 db:=newTestDB(t)
 m:=initialModel(db,time.Date(2026,10,11,10,0,0,0,time.UTC))
 m.notes=[]Note{{ID:0,Content:"untimed"}}
 m.buffer=stringToBuffer("untimed")
 at:=time.Date(2026,10,11,10,0,1,0,time.UTC)
 changed,_:=m.Update(tickMsg(at))
 got:=changed.(model)
 if !got.timerNow.Equal(at) {t.Fatalf("timer clock=%s want %s",got.timerNow,at)}
}
