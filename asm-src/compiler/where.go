package main

// Where a runtime error happened.
//
//	runtime error: index 5 is out of range for a list of length 3
//	    at game.scd:42 in update
//
// The first line is the Go backend's, byte for byte, as it always was.
// The second is new, and costs nothing until something fails: each
// place that can fail - an index checked against its length, a must, an
// abort - knows at compile time which statement it is in, so it is
// handed that statement's file, line and function as a string constant,
// written to a runtime word just before the error routine is called.
// Nothing is stored as the program runs normally, so a loop pays
// nothing for it, and a call made earlier in the same statement cannot
// leave a stale answer behind.
//
// Crashes that are not Scandiacus's runtime errors - an access violation from
// a bad address given to mem.*, a native function misbehaving - are
// caught by crash.go instead, which names the function they happened
// in.

import (
	"fmt"
	"path/filepath"
)

// where is the location string for a failure being lowered now, as an
// index into the string table plus one, or 0 when there is nothing to
// say: code the compiler wrote itself, such as the prelude, has no line
// of the program's.
func (l *lowerer) where() int64 {
	if l.curFile == "" || l.curFile == "<prelude>" || l.curLine == 0 {
		return 0
	}
	name := l.fnDisplay
	if name == "" {
		name = l.fn.Name
	}
	text := fmt.Sprintf("%s:%d in %s", filepath.Base(l.curFile), l.curLine, name)
	return l.mod.intern(text) + 1
}

// where stores a failure's location in the runtime word the error
// routines read, when it has one.
func (e *Emitter) where(in Instr) {
	if in.Imm <= 0 {
		return
	}
	e.line("lea rax, __str%d[rip]", in.Imm-1)
	e.line("lea rdx, __globals[rip]")
	e.line("mov qword ptr [rdx+%d], rax", gcWhereSlot*wordSize)
}

// whereRoutine prints the location line, if a location was stored.
const whereRoutine = `
__vy_where:
    push rbp
    mov rbp, rsp
    sub rsp, 256
    lea rcx, __globals[rip]
    mov r9, qword ptr [rcx+72]
    test r9, r9
    je .Lwhere_done
    lea rcx, [rbp-200]
    mov rdx, 180
    lea r8, __fmt_at[rip]
    call _snprintf
    mov r8d, eax
    cmp r8d, 180
    jbe .Lwhere_write
    mov r8d, 180
.Lwhere_write:
    mov ecx, 2
    lea rdx, [rbp-200]
    call _write
.Lwhere_done:
    mov rsp, rbp
    pop rbp
    ret
`
