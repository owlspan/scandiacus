package main

// Regular expressions, in Veyl.
//
// A pattern is parsed into a small program and run by a backtracking
// VM. That is what Go's regexp gives for the same pattern: leftmost, and
// among matches at the same start, the one a backtracking search would
// have found first. Matching the semantics matters more here than
// matching the algorithm, since the two backends have to agree.
//
// The program is a flat []int, three slots per instruction: op, x, y.
// A character class refers back into the pattern by offset rather than
// carrying its own text, so the whole thing is one list of numbers.
//
// What it does not do: non-capturing groups, named groups, lookaround,
// backreferences, the (?i) flags, and Unicode classes. Bytes, not runes.

const preludeRe = `
// op codes
fn __vy_reCHAR() -> int { return 0 }
fn __vy_reANY() -> int { return 1 }
fn __vy_reCLASS() -> int { return 2 }
fn __vy_reSPLIT() -> int { return 3 }
fn __vy_reJMP() -> int { return 4 }
fn __vy_reSAVE() -> int { return 5 }
fn __vy_reMATCH() -> int { return 6 }
fn __vy_reBOL() -> int { return 7 }
fn __vy_reEOL() -> int { return 8 }
fn __vy_reESC() -> int { return 9 }

// A fragment is a program whose jump targets are relative to its own
// start, so two of them concatenate by shifting the second.
fn __vy_reFrag(op: int, x: int, y: int) -> []int {
    return [op, x, y]
}

fn __vy_reLen(f: []int) -> int {
    return len(f) / 3
}

fn __vy_reCat(a: []int, b: []int) -> []int {
    let out: []int = []
    let i = 0
    while i < len(a) {
        push(out, a[i])
        i = i + 1
    }
    let shift = __vy_reLen(a)
    i = 0
    while i < len(b) {
        let op = b[i]
        let x = b[i + 1]
        let y = b[i + 2]
        if op == __vy_reSPLIT() {
            x = x + shift
            y = y + shift
        }
        if op == __vy_reJMP() { x = x + shift }
        push(out, op)
        push(out, x)
        push(out, y)
        i = i + 3
    }
    return out
}

fn __vy_reAltFrag(a: []int, b: []int) -> []int {
    let la = __vy_reLen(a)
    let lb = __vy_reLen(b)
    // split 1, la+2 ; a ; jmp la+2+lb ; b
    let head = __vy_reFrag(__vy_reSPLIT(), 1, la + 2)
    let mid = __vy_reFrag(__vy_reJMP(), la + 2 + lb, 0)
    let out = __vy_reCatRaw(head, a, 1)
    out = __vy_reCatRaw(out, mid, 0)
    out = __vy_reCatRaw(out, b, la + 2)
    return out
}

// Concatenate with an explicit shift, for the cases above where the
// targets were worked out in advance.
fn __vy_reCatRaw(a: []int, b: []int, shift: int) -> []int {
    let out: []int = []
    let i = 0
    while i < len(a) {
        push(out, a[i])
        i = i + 1
    }
    i = 0
    while i < len(b) {
        let op = b[i]
        let x = b[i + 1]
        let y = b[i + 2]
        if op == __vy_reSPLIT() {
            x = x + shift
            y = y + shift
        }
        if op == __vy_reJMP() { x = x + shift }
        push(out, op)
        push(out, x)
        push(out, y)
        i = i + 3
    }
    return out
}

// x* - but when x can match nothing it is (x+)?, which is how Go
// compiles it: with one split doing both the entry and the going round
// again, an x that matches nothing would bring the search back to that
// split at once, and the order Go tries things in would be different.
fn __vy_reStar(a: []int, greedy: bool) -> []int {
    if __vy_reNullable(a) {
        return __vy_reOpt(__vy_rePlus(a, greedy), greedy)
    }
    let la = __vy_reLen(a)
    let first = 1
    let second = la + 2
    if !greedy {
        first = la + 2
        second = 1
    }
    let head = __vy_reFrag(__vy_reSPLIT(), first, second)
    let out = __vy_reCatRaw(head, a, 1)
    out = __vy_reCatRaw(out, __vy_reFrag(__vy_reJMP(), 0, 0), 0)
    return out
}

fn __vy_rePlus(a: []int, greedy: bool) -> []int {
    let la = __vy_reLen(a)
    let first = 0
    let second = la + 1
    if !greedy {
        first = la + 1
        second = 0
    }
    return __vy_reCatRaw(a, __vy_reFrag(__vy_reSPLIT(), first, second), 0)
}

fn __vy_reOpt(a: []int, greedy: bool) -> []int {
    let la = __vy_reLen(a)
    let first = 1
    let second = la + 1
    if !greedy {
        first = la + 1
        second = 1
    }
    return __vy_reCatRaw(__vy_reFrag(__vy_reSPLIT(), first, second), a, 1)
}

// Whether a fragment can match without consuming anything: a path from
// its start to its end through jumps, splits, saves and anchors only.
fn __vy_reNullable(f: []int) -> bool {
    let n = __vy_reLen(f)
    let seen: []bool = []
    let i = 0
    while i <= n {
        push(seen, false)
        i = i + 1
    }
    let todo: []int = [0]
    while len(todo) > 0 {
        let pc = pop(todo)
        if pc >= n { return true }
        if seen[pc] { continue }
        seen[pc] = true
        let op = f[pc * 3]
        if op == __vy_reSPLIT() {
            push(todo, f[pc * 3 + 1])
            push(todo, f[pc * 3 + 2])
        } else if op == __vy_reJMP() {
            push(todo, f[pc * 3 + 1])
        } else if op == __vy_reSAVE() || op == __vy_reBOL() || op == __vy_reEOL() {
            push(todo, pc + 1)
        }
    }
    return false
}

// st holds the parser position, the next group number and an error flag.
fn __vy_reAt(pat: str, st: []int) -> int {
    if st[0] >= len(pat) { return -1 }
    return __strAt(pat, st[0])
}

fn __vy_reAlt(pat: str, st: []int) -> []int {
    let out = __vy_reConcat(pat, st)
    while __vy_reAt(pat, st) == 124 {
        if st[2] != 0 { return out }
        st[0] = st[0] + 1
        out = __vy_reAltFrag(out, __vy_reConcat(pat, st))
    }
    return out
}

fn __vy_reConcat(pat: str, st: []int) -> []int {
    let out: []int = []
    while true {
        let c = __vy_reAt(pat, st)
        if c < 0 || c == 124 || c == 41 { return out }
        let before = st[0]
        out = __vy_reCat(out, __vy_reRepeat(pat, st))
        // stop on a bad pattern. reAtom flags the error without moving
        // the position, so without this the loop spins and allocates.
        if st[2] != 0 { return out }
        if st[0] == before {
            st[2] = 1
            return out
        }
    }
    return out
}

fn __vy_reRepeat(pat: str, st: []int) -> []int {
    let atom = __vy_reAtom(pat, st)
    if st[2] != 0 { return atom }

    let c = __vy_reAt(pat, st)
    if c == 42 || c == 43 || c == 63 {
        st[0] = st[0] + 1
        let greedy = true
        if __vy_reAt(pat, st) == 63 {
            greedy = false
            st[0] = st[0] + 1
        }
        if c == 42 { return __vy_reStar(atom, greedy) }
        if c == 43 { return __vy_rePlus(atom, greedy) }
        return __vy_reOpt(atom, greedy)
    }
    if c == 123 {
        return __vy_reCounted(pat, st, atom)
    }
    return atom
}

// {n}, {n,} and {n,m}, built by repeating the fragment.
fn __vy_reCounted(pat: str, st: []int, atom: []int) -> []int {
    let save = st[0]
    st[0] = st[0] + 1

    let lo = __vy_reNumber(pat, st)
    if lo < 0 {
        // Not a quantifier after all, so the brace is a literal.
        st[0] = save
        return atom
    }
    let hi = lo
    if __vy_reAt(pat, st) == 44 {
        st[0] = st[0] + 1
        hi = __vy_reNumber(pat, st)
    }
    if __vy_reAt(pat, st) != 125 {
        st[0] = save
        return atom
    }
    st[0] = st[0] + 1

    let out: []int = []
    let i = 0
    while i < lo {
        out = __vy_reCat(out, atom)
        i = i + 1
    }
    if hi < 0 {
        return __vy_reCat(out, __vy_reStar(atom, true))
    }
    i = lo
    while i < hi {
        out = __vy_reCat(out, __vy_reOpt(atom, true))
        i = i + 1
    }
    return out
}

fn __vy_reNumber(pat: str, st: []int) -> int {
    let n = -1
    while true {
        let c = __vy_reAt(pat, st)
        if c < 48 || c > 57 { return n }
        if n < 0 { n = 0 }
        n = n * 10 + (c - 48)
        st[0] = st[0] + 1
    }
    return n
}

fn __vy_reAtom(pat: str, st: []int) -> []int {
    let c = __vy_reAt(pat, st)
    if c < 0 {
        st[2] = 1
        return []
    }

    // (
    if c == 40 {
        st[0] = st[0] + 1
        let g = st[1]
        st[1] = st[1] + 1
        let inner = __vy_reAlt(pat, st)
        if __vy_reAt(pat, st) != 41 {
            st[2] = 1
            return []
        }
        st[0] = st[0] + 1
        let out = __vy_reFrag(__vy_reSAVE(), g * 2, 0)
        out = __vy_reCat(out, inner)
        return __vy_reCat(out, __vy_reFrag(__vy_reSAVE(), g * 2 + 1, 0))
    }

    // [
    if c == 91 {
        let start = st[0] + 1
        let i = start
        if i < len(pat) && __strAt(pat, i) == 94 { i = i + 1 }
        // A ] straight after the opening bracket is a literal ].
        if i < len(pat) && __strAt(pat, i) == 93 { i = i + 1 }
        while i < len(pat) && __strAt(pat, i) != 93 {
            if __strAt(pat, i) == 92 { i = i + 1 }
            i = i + 1
        }
        if i >= len(pat) {
            st[2] = 1
            return []
        }
        st[0] = i + 1
        return __vy_reFrag(__vy_reCLASS(), start, i)
    }

    if c == 46 {
        st[0] = st[0] + 1
        return __vy_reFrag(__vy_reANY(), 0, 0)
    }
    if c == 94 {
        st[0] = st[0] + 1
        return __vy_reFrag(__vy_reBOL(), 0, 0)
    }
    if c == 36 {
        st[0] = st[0] + 1
        return __vy_reFrag(__vy_reEOL(), 0, 0)
    }
    // ) * + ? { } on their own are a malformed pattern.
    if c == 41 || c == 42 || c == 43 || c == 63 {
        st[2] = 1
        return []
    }

    if c == 92 {
        st[0] = st[0] + 1
        let e = __vy_reAt(pat, st)
        if e < 0 {
            st[2] = 1
            return []
        }
        st[0] = st[0] + 1
        if e == 100 || e == 68 || e == 115 || e == 83 || e == 119 || e == 87 {
            return __vy_reFrag(__vy_reESC(), e, 0)
        }
        return __vy_reFrag(__vy_reCHAR(), __vy_reUnescape(e), 0)
    }

    st[0] = st[0] + 1
    return __vy_reFrag(__vy_reCHAR(), c, 0)
}

fn __vy_reUnescape(e: int) -> int {
    if e == 110 { return 10 }
    if e == 116 { return 9 }
    if e == 114 { return 13 }
    if e == 102 { return 12 }
    if e == 118 { return 11 }
    if e == 48 { return 0 }
    if e == 97 { return 7 }
    return e
}

// Compile to a finished program, or an empty list when the pattern is
// malformed. Slot 0 of the result is the group count.
fn __vy_reCompile(pat: str) -> []int {
    let st: []int = [0, 1, 0]
    let frag = __vy_reAlt(pat, st)
    if st[2] != 0 || st[0] != len(pat) { return [] }

    // Group 0 is the whole match.
    let body = __vy_reCatRaw(__vy_reFrag(__vy_reSAVE(), 0, 0), frag, 1)
    body = __vy_reCatRaw(body, __vy_reFrag(__vy_reSAVE(), 1, 0), 0)
    body = __vy_reCatRaw(body, __vy_reFrag(__vy_reMATCH(), 0, 0), 0)

    let out: []int = [st[1]]
    let i = 0
    while i < len(body) {
        push(out, body[i])
        i = i + 1
    }
    return out
}

fn __vy_reValid(pat: str) -> bool {
    return len(__vy_reCompile(pat)) > 0
}

// ---- the matcher ----

fn __vy_reClassHas(pat: str, from: int, to: int, ch: int) -> bool {
    let i = from
    let neg = false
    if i < to && __strAt(pat, i) == 94 {
        neg = true
        i = i + 1
    }

    let hit = false
    while i < to {
        let c = __strAt(pat, i)
        if c == 92 && i + 1 < to {
            let e = __strAt(pat, i + 1)
            if e == 100 || e == 68 || e == 115 || e == 83 || e == 119 || e == 87 {
                if __vy_reEscHas(e, ch) { hit = true }
            } else {
                if __vy_reUnescape(e) == ch { hit = true }
            }
            i = i + 2
        } else {
            if i + 2 < to && __strAt(pat, i + 1) == 45 && __strAt(pat, i + 2) != 93 {
                if ch >= c && ch <= __strAt(pat, i + 2) { hit = true }
                i = i + 3
            } else {
                if c == ch { hit = true }
                i = i + 1
            }
        }
    }
    if neg { return !hit }
    return hit
}

fn __vy_reEscHas(e: int, ch: int) -> bool {
    if e == 100 { return ch >= 48 && ch <= 57 }
    if e == 68 { return !(ch >= 48 && ch <= 57) }
    if e == 115 { return ch == 32 || ch == 9 || ch == 10 || ch == 13 || ch == 11 || ch == 12 }
    if e == 83 { return !(ch == 32 || ch == 9 || ch == 10 || ch == 13 || ch == 11 || ch == 12) }
    if e == 119 { return __vy_reWord(ch) }
    if e == 87 { return !__vy_reWord(ch) }
    return false
}

fn __vy_reWord(ch: int) -> bool {
    if ch >= 48 && ch <= 57 { return true }
    if ch >= 65 && ch <= 90 { return true }
    if ch >= 97 && ch <= 122 { return true }
    return ch == 95
}

// The VM, run from pc at sp. Returns the end position of a match, or
// -1. It is a loop over a stack of jobs rather than a recursion, as Go's
// backtracker is: a split pushes its second way and goes on with the
// first, and a save pushes the old value to put back if the path fails.
// Recursing once per character ran out of stack on a few thousand.
//
// A search never takes the same split twice at the same position: the
// second arrival fails, whichever path it comes by, and the mark stays
// for the rest of the search. That too is Go's backtracker. It ends a
// loop whose body matched nothing - (a*)* went round forever - it makes
// the work at most the program's size times the text's, where
// (a+)+b on a line of a's was exponential, and it decides which paths
// are cut the way Go does, so matches and captures come out the same.
// Only splits are marked: every other instruction has one way on.
// seen is a byte for each split at each position, ord the split number
// of each instruction, set the marks made, which the next search clears.
fn __vy_reStep(prog: []int, pat: str, text: str, pc0: int, sp0: int, caps: []int, seen: bytes, ord: []int, set: []int) -> int {
    let n = len(text)
    // Jobs are three words: 0, pc, sp to try a path; 1, slot, value to
    // put a capture back.
    let jobs: []int = [0, pc0, sp0]
    while len(jobs) > 0 {
        let b = pop(jobs)
        let a = pop(jobs)
        let kind = pop(jobs)
        if kind == 1 {
            caps[a] = b
            continue
        }
        let pc = a
        let sp = b
        let going = true
        while going {
            let at = 1 + pc * 3
            let op = prog[at]
            let x = prog[at + 1]
            if op == __vy_reMATCH() { return sp }
            if op == __vy_reCHAR() {
                if sp >= n || __strAt(text, sp) != x {
                    going = false
                } else {
                    pc = pc + 1
                    sp = sp + 1
                }
            } else if op == __vy_reANY() {
                if sp >= n || __strAt(text, sp) == 10 {
                    going = false
                } else {
                    pc = pc + 1
                    sp = sp + 1
                }
            } else if op == __vy_reCLASS() {
                if sp >= n || !__vy_reClassHas(pat, x, prog[at + 2], __strAt(text, sp)) {
                    going = false
                } else {
                    pc = pc + 1
                    sp = sp + 1
                }
            } else if op == __vy_reESC() {
                if sp >= n || !__vy_reEscHas(x, __strAt(text, sp)) {
                    going = false
                } else {
                    pc = pc + 1
                    sp = sp + 1
                }
            } else if op == __vy_reSPLIT() {
                let mark = ord[pc] * (n + 1) + sp
                if __byteAt(seen, mark) != 0 {
                    going = false
                } else {
                    __bytePut(seen, mark, 1)
                    push(set, mark)
                    push(jobs, 0)
                    push(jobs, prog[at + 2])
                    push(jobs, sp)
                    pc = x
                }
            } else if op == __vy_reJMP() {
                pc = x
            } else if op == __vy_reSAVE() {
                push(jobs, 1)
                push(jobs, x)
                push(jobs, caps[x])
                caps[x] = sp
                pc = pc + 1
            } else if op == __vy_reBOL() {
                if sp != 0 {
                    going = false
                } else {
                    pc = pc + 1
                }
            } else if op == __vy_reEOL() {
                if sp != n {
                    going = false
                } else {
                    pc = pc + 1
                }
            } else {
                going = false
            }
        }
    }
    return -1
}

// The split number of each instruction, -1 for the rest.
fn __vy_reOrd(prog: []int) -> []int {
    let out: []int = []
    let n = 0
    let pc = 1
    while pc < len(prog) {
        if prog[pc] == __vy_reSPLIT() {
            push(out, n)
            n = n + 1
        } else {
            push(out, -1)
        }
        pc = pc + 3
    }
    return out
}

// A byte for every split at every position of text.
fn __vy_reSeen(ord: []int, text: str) -> bytes {
    let splits = 0
    for o in ord {
        if o >= 0 { splits = splits + 1 }
    }
    return __bytesMake(splits * (len(text) + 1) + 1)
}

fn __vy_reCaps(prog: []int) -> []int {
    let caps: []int = []
    let i = 0
    while i < prog[0] * 2 {
        push(caps, -1)
        i = i + 1
    }
    return caps
}

// The leftmost match at or after start. caps comes back filled in;
// the result is the start position, or -1.
fn __vy_reSearch(prog: []int, pat: str, text: str, start: int, caps: []int, seen: bytes, ord: []int, set: []int) -> int {
    // What the last search marked is cleared. Within this one the marks
    // stay, across start positions too, as in Go: a split that failed
    // from one start fails from the next.
    while len(set) > 0 {
        __bytePut(seen, pop(set), 0)
    }
    let at = start
    while at <= len(text) {
        let i = 0
        while i < len(caps) {
            caps[i] = -1
            i = i + 1
        }
        if __vy_reStep(prog, pat, text, 0, at, caps, seen, ord, set) >= 0 { return at }
        at = at + 1
    }
    return -1
}

fn __vy_reBadPattern(pat: str) -> str {
    return __abortStr("\"" + pat + "\" is not a valid pattern")
}

// ---- the library ----

fn __vy_reMatches(pat: str, text: str) -> bool {
    let prog = __vy_reCompile(pat)
    if len(prog) == 0 { __vy_reBadPattern(pat) }
    let caps = __vy_reCaps(prog)
    let ord = __vy_reOrd(prog)
    let seen = __vy_reSeen(ord, text)
    let set: []int = []
    return __vy_reSearch(prog, pat, text, 0, caps, seen, ord, set) >= 0
}

fn __vy_reFind(pat: str, text: str) -> str {
    let prog = __vy_reCompile(pat)
    if len(prog) == 0 { __vy_reBadPattern(pat) }
    let caps = __vy_reCaps(prog)
    let ord = __vy_reOrd(prog)
    let seen = __vy_reSeen(ord, text)
    let set: []int = []
    if __vy_reSearch(prog, pat, text, 0, caps, seen, ord, set) < 0 { return "" }
    return __substrB(text, caps[0], caps[1])
}

fn __vy_reFindAll(pat: str, text: str) -> []str {
    let prog = __vy_reCompile(pat)
    if len(prog) == 0 { __vy_reBadPattern(pat) }
    let out: []str = []
    let caps = __vy_reCaps(prog)
    let ord = __vy_reOrd(prog)
    let seen = __vy_reSeen(ord, text)
    let set: []int = []

    let at = 0
    let prevEnd = -1
    while at <= len(text) {
        if __vy_reSearch(prog, pat, text, at, caps, seen, ord, set) < 0 { return out }
        let s = caps[0]
        let e = caps[1]
        let empty = e == s
        // Go drops an empty match sitting where the last one ended, so
        // drop it here too or the two backends disagree.
        if !empty || s != prevEnd {
            push(out, __substrB(text, s, e))
        }
        prevEnd = e
        // An empty match still has to move, or this never ends.
        if empty {
            at = e + 1
        } else {
            at = e
        }
    }
    return out
}

fn __vy_reGroups(pat: str, text: str) -> []str {
    let prog = __vy_reCompile(pat)
    if len(prog) == 0 { __vy_reBadPattern(pat) }
    let out: []str = []
    let caps = __vy_reCaps(prog)
    let ord = __vy_reOrd(prog)
    let seen = __vy_reSeen(ord, text)
    let set: []int = []
    if __vy_reSearch(prog, pat, text, 0, caps, seen, ord, set) < 0 { return out }

    let g = 1
    while g < prog[0] {
        if caps[g * 2] < 0 {
            push(out, "")
        } else {
            push(out, __substrB(text, caps[g * 2], caps[g * 2 + 1]))
        }
        g = g + 1
    }
    return out
}

fn __vy_reCount(pat: str, text: str) -> int {
    return len(__vy_reFindAll(pat, text))
}

// Go's Split exactly: the matches FindAll finds; the text before each,
// except before an empty match at the very start; and what follows the
// last, unless the last began at the end of the text.
fn __vy_reSplit(pat: str, text: str) -> []str {
    let prog = __vy_reCompile(pat)
    if len(prog) == 0 { __vy_reBadPattern(pat) }
    let out: []str = []
    if len(text) == 0 {
        if len(pat) > 0 { push(out, "") }
        return out
    }
    let caps = __vy_reCaps(prog)
    let ord = __vy_reOrd(prog)
    let seen = __vy_reSeen(ord, text)
    let set: []int = []
    let beg = 0
    let end = 0
    let at = 0
    let prevEnd = -1
    while at <= len(text) {
        if __vy_reSearch(prog, pat, text, at, caps, seen, ord, set) < 0 { break }
        let s = caps[0]
        let e = caps[1]
        let empty = e == s
        if !empty || s != prevEnd {
            end = s
            if e != 0 {
                push(out, __substrB(text, beg, end))
            }
            beg = e
        }
        prevEnd = e
        if empty {
            at = e + 1
        } else {
            at = e
        }
    }
    if end != len(text) {
        push(out, __substrB(text, beg, len(text)))
    }
    return out
}

fn __vy_reReplace(pat: str, text: str, repl: str) -> str {
    let prog = __vy_reCompile(pat)
    if len(prog) == 0 { __vy_reBadPattern(pat) }
    let out = ""
    let caps = __vy_reCaps(prog)
    let ord = __vy_reOrd(prog)
    let seen = __vy_reSeen(ord, text)
    let set: []int = []

    let lastEnd = 0
    let at = 0
    while at <= len(text) {
        if __vy_reSearch(prog, pat, text, at, caps, seen, ord, set) < 0 {
            return out + __substrB(text, lastEnd, len(text))
        }
        let s = caps[0]
        let e = caps[1]
        out = out + __substrB(text, lastEnd, s)
        // no replacement for an empty match right after another one
        if e > lastEnd || s == 0 {
            out = out + __vy_reExpand(repl, text, caps)
        }
        lastEnd = e
        // always move at least one character
        if at + 1 > e {
            at = at + 1
        } else {
            at = e
        }
    }
    return out + __substrB(text, lastEnd, len(text))
}

// $1 and ${1} in a replacement, and $$ for a literal dollar.
fn __vy_reExpand(repl: str, text: str, caps: []int) -> str {
    let out = ""
    let i = 0
    while i < len(repl) {
        let c = __strAt(repl, i)
        if c != 36 {
            out = out + __charAtB(repl, i)
            i = i + 1
        } else {
            if i + 1 < len(repl) && __strAt(repl, i + 1) == 36 {
                out = out + "$"
                i = i + 2
            } else {
                let j = i + 1
                let braced = false
                if j < len(repl) && __strAt(repl, j) == 123 {
                    braced = true
                    j = j + 1
                }
                let n = -1
                while j < len(repl) && __strAt(repl, j) >= 48 && __strAt(repl, j) <= 57 {
                    if n < 0 { n = 0 }
                    n = n * 10 + (__strAt(repl, j) - 48)
                    j = j + 1
                }
                if braced && j < len(repl) && __strAt(repl, j) == 125 { j = j + 1 }
                if n < 0 {
                    out = out + "$"
                    i = i + 1
                } else {
                    if n * 2 + 1 < len(caps) && caps[n * 2] >= 0 {
                        out = out + __substrB(text, caps[n * 2], caps[n * 2 + 1])
                    }
                    i = j
                }
            }
        }
    }
    return out
}

// QuoteMeta: every character Go escapes, escaped.
fn __vy_reEscape(s: str) -> str {
    let special = "\\.+*?()|[]{{}}^$"
    let out = ""
    let i = 0
    while i < len(s) {
        let c = __charAtB(s, i)
        if contains(special, c) { out = out + "\\" }
        out = out + c
        i = i + 1
    }
    return out
}
`
