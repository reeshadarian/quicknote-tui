package main

import (
 "unicode/utf8"
 "github.com/rivo/uniseg"
)

// Cursor coordinates are rune offsets, never UTF-8 byte offsets.
// Grapheme boundaries avoid splitting combining marks, ZWJ emoji and flags.
// The helpers tolerate out-of-range offsets and keep their return in [0,len(line)].
func graphemeBoundaries(line []rune) []int {
 boundaries:=[]int{0}
 g:=uniseg.NewGraphemes(string(line))
 at:=0
 for g.Next() {
  at+=utf8.RuneCountInString(g.Str())
  boundaries=append(boundaries,at)
 }
 return boundaries
}
func previousGraphemeBoundary(line []rune, col int) int {
 if col<=0 {return 0}
 if col>len(line) {col=len(line)}
 previous:=0
 for _, boundary:=range graphemeBoundaries(line) {
  if boundary>=col {return previous}
  previous=boundary
 }
 return previous
}
func nextGraphemeBoundary(line []rune, col int) int {
 if col<0 {col=0}
 if col>=len(line) {return len(line)}
 for _,boundary:=range graphemeBoundaries(line) {
  if boundary>col {return boundary}
 }
 return len(line)
}
