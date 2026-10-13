package main

// SQLite, through sqlite3.dll.
//
//	let conn = must(db.open("app.db"))
//	must(db.exec(conn, "create table if not exists notes (id integer primary key, body text)", []))
//	must(db.exec(conn, "insert into notes (body) values (?)", ["hello"]))
//	for row in must(db.query(conn, "select id, body from notes where body like ?", ["h%"])) {
//	    print("{row[0]}: {row[1]}")
//	}
//	db.close(conn)
//
// The library is here in the compiler; the DLL comes from a package.
// `veyl get sqlite` installs sqlite3.dll into veyl_modules, and
// `veyl build` copies it next to the executable. A program that never
// calls db.* never imports it and never carries it, which is the whole
// reason SQLite is not in the installer.
//
// Every statement goes through prepare/bind/step. There is no path that
// pastes a value into SQL, so there is no way to write an injection
// through this API even by accident: db.exec and db.query both take
// their arguments as a separate list.
//
// Values come back as str. SQLite columns are dynamically typed and a
// row could hold anything, so one type out is the honest answer until
// there is a way to say what a query returns. toInt and float() are
// there for the caller.

const (
	sqliteOK   = 0
	sqliteRow  = 100
	sqliteDone = 101

	// SQLITE_TRANSIENT: sqlite copies the text rather than keeping the
	// pointer. The alternative is SQLITE_STATIC, which would mean the
	// string had to outlive the statement, and here it does not.
	sqliteTransient = -1

	// open flags: READWRITE | CREATE
	sqliteOpenReadWrite = 0x00000002
	sqliteOpenCreate    = 0x00000004
)

var sqliteSyms = []string{
	"sqlite3_open_v2", "sqlite3_close", "sqlite3_errmsg",
	"sqlite3_prepare_v2", "sqlite3_bind_text", "sqlite3_step",
	"sqlite3_column_count", "sqlite3_column_text", "sqlite3_finalize",
	"sqlite3_changes", "sqlite3_last_insert_rowid", "sqlite3_libversion",
}

func (l *lowerer) dbBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	switch name {
	case "db.open":
		if !arity(1) {
			return l.junk(), true
		}
		return l.dbOpen(l.expr(c.Args[0])), true

	case "db.close":
		if !arity(1) {
			return l.junk(), true
		}
		l.ccall("sqlite3_close", []Reg{l.expr(c.Args[0])},
			[]vty{vInt}, vInt, true, false)
		return l.junk(), true

	case "db.exec":
		if !arity(3) {
			return l.junk(), true
		}
		return l.dbRun(l.expr(c.Args[0]), l.expr(c.Args[1]), l.expr(c.Args[2]), false), true

	case "db.query":
		if !arity(3) {
			return l.junk(), true
		}
		return l.dbRun(l.expr(c.Args[0]), l.expr(c.Args[1]), l.expr(c.Args[2]), true), true

	case "db.changes":
		if !arity(1) {
			return l.junk(), true
		}
		return l.ccall("sqlite3_changes", []Reg{l.expr(c.Args[0])},
			[]vty{vInt}, vInt, true, false), true

	case "db.lastId":
		if !arity(1) {
			return l.junk(), true
		}
		return l.ccall("sqlite3_last_insert_rowid", []Reg{l.expr(c.Args[0])},
			[]vty{vInt}, vInt, false, false), true

	case "db.version":
		v := l.ccall("sqlite3_libversion", nil, nil, vInt, false, false)
		return l.cstr(v), true
	}
	return NoReg, false
}

// dbFail builds the failure sqlite reports, which is a sentence rather
// than a code: "no such table: notes" beats "error 1".
func (l *lowerer) dbFail(conn Reg, what string, t vty) Reg {
	msg := l.ccall("sqlite3_errmsg", []Reg{conn}, []vty{vInt}, vInt, false, false)
	return l.resFail(l.concatAll(l.strLit(what+": "), l.cstr(msg)), t)
}

func (l *lowerer) dbOpen(path Reg) Reg {
	ret := vty{k: kInt, res: true}
	sym := l.helperFunc("dbopen", []vty{vStr}, ret, func(a []Reg) {
		// sqlite writes the handle through a pointer, so it needs a word
		// to write into rather than returning it.
		slot := l.allocObj(l.constant(wordSize), tagBytes)
		l.emit(Instr{Op: OpStoreMem, A: slot, B: l.constant(0), Imm: 0})

		rc := l.ccall("sqlite3_open_v2",
			[]Reg{a[0], slot,
				l.constant(sqliteOpenReadWrite | sqliteOpenCreate),
				l.constant(0)},
			[]vty{vStr, vInt, vInt, vInt}, vInt, true, false)
		conn := l.field(slot, 0, vInt)

		ok := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, rc, l.constant(sqliteOK)),
			Dst: NoReg, Imm: ok})
		// A failed open still gives a handle, and it is the only thing
		// that knows why. Closing it after reading the message is what
		// sqlite's own documentation says to do.
		bad := l.dbFail(conn, "cannot open the database", ret)
		l.ccall("sqlite3_close", []Reg{conn}, []vty{vInt}, vInt, true, false)
		l.emit(Instr{Op: OpRet, A: bad, Dst: NoReg})

		l.mark(ok)
		l.emit(Instr{Op: OpRet, A: l.resOk(conn, ret), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{path}, []vty{vStr}, ret)
}

// dbRun prepares, binds, steps and collects.
//
// One routine for exec and query because they differ only in whether
// the rows are kept: a query that changes nothing and an insert that
// returns nothing run the same three calls.
func (l *lowerer) dbRun(conn, sql, args Reg, wantRows bool) Reg {
	rows := vListOf(vListOf(vStr))
	ret := rows
	ret.res = true
	if !wantRows {
		ret = vty{k: kVoid, res: true}
	}

	name := "dbexec"
	if wantRows {
		name = "dbquery"
	}

	sym := l.helperFunc(name,
		[]vty{vInt, vStr, vListOf(vStr)}, ret, func(a []Reg) {
			conn, sql, args := a[0], a[1], a[2]

			stmtSlot := l.allocObj(l.constant(wordSize), tagBytes)
			l.emit(Instr{Op: OpStoreMem, A: stmtSlot, B: l.constant(0), Imm: 0})

			rc := l.ccall("sqlite3_prepare_v2",
				[]Reg{conn, sql, l.constant(-1), stmtSlot, l.constant(0)},
				[]vty{vInt, vStr, vInt, vInt, vInt}, vInt, true, false)

			prepared := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, rc, l.constant(sqliteOK)),
				Dst: NoReg, Imm: prepared})
			l.emit(Instr{Op: OpRet, A: l.dbFail(conn, "cannot prepare", ret), Dst: NoReg})
			l.mark(prepared)

			stmt := l.field(stmtSlot, 0, vInt)

			// Bind. Parameters are one-based, which is the one place
			// sqlite counts from one and the easiest thing to get wrong.
			n := l.field(args, listLenOff, vInt)
			i := l.temp(vInt)
			l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})
			bindTop := l.newLabel()
			bindDone := l.newLabel()
			l.mark(bindTop)
			cur := l.load(i, vInt)
			l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, n),
				Dst: NoReg, Imm: bindDone})
			v := l.listGet(args, cur, vStr)
			l.ccall("sqlite3_bind_text",
				[]Reg{stmt, l.arith(OpAdd, cur, l.constant(1)), v,
					l.constant(-1), l.constant(sqliteTransient)},
				[]vty{vInt, vInt, vStr, vInt, vInt}, vInt, true, false)
			l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(1)),
				Dst: NoReg, Imm: i})
			l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: bindTop})
			l.mark(bindDone)

			out := l.newList(rows, 0)

			// Step until done, collecting a row each time round.
			stepTop := l.newLabel()
			stepDone := l.newLabel()
			l.mark(stepTop)
			step := l.blockingCall("sqlite3_step", []Reg{stmt}, []vty{vInt}, vInt, true)
			l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, step, l.constant(sqliteRow)),
				Dst: NoReg, Imm: stepDone})

			if wantRows {
				cols := l.ccall("sqlite3_column_count", []Reg{stmt},
					[]vty{vInt}, vInt, true, false)
				row := l.newList(vListOf(vStr), 0)
				ci := l.temp(vInt)
				l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: ci})
				colTop := l.newLabel()
				colDone := l.newLabel()
				l.mark(colTop)
				cc := l.load(ci, vInt)
				l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cc, cols),
					Dst: NoReg, Imm: colDone})
				text := l.ccall("sqlite3_column_text", []Reg{stmt, cc},
					[]vty{vInt, vInt}, vInt, false, false)
				// A NULL column gives a null pointer, which would be a
				// crash the first time a row has one. Empty string is
				// what the Go backend's database rows give too.
				safe := l.pick(l.compare(OpEq, text, l.constant(0)),
					l.emptyStr(), text, vStr)
				l.regTy[safe] = vStr
				// sqlite owns that buffer and reuses it on the next
				// step, so the string has to be copied out now.
				l.listPush(row, l.dupStr(safe))
				l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cc, l.constant(1)),
					Dst: NoReg, Imm: ci})
				l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: colTop})
				l.mark(colDone)
				l.listPush(out, row)
			}

			l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: stepTop})
			l.mark(stepDone)

			l.ccall("sqlite3_finalize", []Reg{stmt}, []vty{vInt}, vInt, true, false)

			good := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, step, l.constant(sqliteDone)),
				Dst: NoReg, Imm: good})
			l.emit(Instr{Op: OpRet, A: l.dbFail(conn, "query failed", ret), Dst: NoReg})

			l.mark(good)
			if wantRows {
				l.emit(Instr{Op: OpRet, A: l.resOk(out, ret), Dst: NoReg})
			} else {
				l.emit(Instr{Op: OpRet, A: l.resOk(l.constant(0), ret), Dst: NoReg})
			}
		})
	return l.callHelper(sym, []Reg{conn, sql, args},
		[]vty{vInt, vStr, vListOf(vStr)}, ret)
}

// dupStr copies a NUL-terminated string into the heap.
//
// Needed wherever a C library hands back a pointer to memory it owns.
// sqlite3_column_text is the case here: the buffer is reused on the
// next step, so a row kept without copying reads as whatever the last
// row happened to be.
func (l *lowerer) dupStr(s Reg) Reg {
	return l.cstr(s)
}
