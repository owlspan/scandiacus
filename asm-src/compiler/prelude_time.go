package main

// The time library, in Scandiacus.
//
// The Go backend spells a format the way Scandiacus users do - YYYY-MM-DD -
// translates that to Go's reference-time layout with a plain textual
// replace, and hands the result to time.Format. Both halves are
// reproduced here, in that order, and the order is the point: a
// translation followed by a layout interpreter behaves differently from
// interpreting the Scandiacus tokens directly, because a literal `Jan` in the
// user's format survives the replace and is then read as a month by the
// layout interpreter. That is arguably a wart on the Go backend, and it
// is the behaviour, so it is the behaviour here.
//
// Everything renders in local time, for the reason in timelib.go.

const preludeTime = `
// The nine fields of C's struct tm, by the index __tmField takes.

fn __vy_tm_sec(t: int) -> int { return __tmField(t, 0) }
fn __vy_tm_min(t: int) -> int { return __tmField(t, 1) }
fn __vy_tm_hour(t: int) -> int { return __tmField(t, 2) }
fn __vy_tm_mday(t: int) -> int { return __tmField(t, 3) }
fn __vy_tm_mon(t: int) -> int { return __tmField(t, 4) + 1 }
fn __vy_tm_year(t: int) -> int { return __tmField(t, 5) + 1900 }
fn __vy_tm_wday(t: int) -> int { return __tmField(t, 6) }

fn __vy_monthName(m: int) -> str {
    let names = ["January", "February", "March", "April", "May", "June",
                 "July", "August", "September", "October", "November", "December"]
    if m < 1 || m > 12 { return "?" }
    return names[m - 1]
}

fn __vy_dayName(d: int) -> str {
    let names = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday",
                 "Friday", "Saturday"]
    if d < 0 || d > 6 { return "?" }
    return names[d]
}

// Zero-padded decimal, which every numeric field of a layout wants.
fn __vy_pad2(n: int) -> str {
    if n < 10 && n >= 0 { return "0" + str(n) }
    return str(n)
}

fn __vy_pad4(n: int) -> str {
    let s = str(n)
    while len(s) < 4 { s = "0" + s }
    return s
}

// The Scandiacus format to Go's reference layout. strings.NewReplacer tries
// each pair in order at each position and takes the first that matches,
// which is why the longer spellings are listed before the shorter ones
// they start with - YYYY before YY, MMMM before MMM before MM.
fn __vy_goLayout(f: str) -> str {
    let from = ["YYYY", "YY", "MMMM", "MMM", "MM", "DDDD", "DDD", "DD",
                "HH", "hh", "mm", "ss", "AM", "ZZ"]
    let to = ["2006", "06", "January", "Jan", "01", "Monday", "Mon", "02",
              "15", "03", "04", "05", "PM", "-07:00"]

    let out = ""
    let i = 0
    while i < len(f) {
        let matched = false
        let k = 0
        while k < len(from) && !matched {
            let pat = from[k]
            if i + len(pat) <= len(f) && __substrB(f, i, i + len(pat)) == pat {
                out = out + to[k]
                i = i + len(pat)
                matched = true
            }
            k = k + 1
        }
        if !matched {
            out = out + __charAtB(f, i)
            i = i + 1
        }
    }
    return out
}

// The layout chunks Go recognises, longest first so that 2006 is not
// read as 20 followed by 06 and January is not read as Jan.
fn __vy_layoutChunks() -> []str {
    return ["2006", "January", "Monday", "Jan", "Mon", "15", "01", "02",
            "03", "04", "05", "PM", "pm", "06", "-07:00", "Z07:00",
            "-0700", "1", "2", "3", "4", "5"]
}

fn __vy_renderChunk(chunk: str, t: int) -> str {
    if chunk == "2006" { return __vy_pad4(__vy_tm_year(t)) }
    if chunk == "06" {
        let y = __vy_tm_year(t) % 100
        return __vy_pad2(y)
    }
    if chunk == "January" { return __vy_monthName(__vy_tm_mon(t)) }
    if chunk == "Jan" { return __substrB(__vy_monthName(__vy_tm_mon(t)), 0, 3) }
    if chunk == "Monday" { return __vy_dayName(__vy_tm_wday(t)) }
    if chunk == "Mon" { return __substrB(__vy_dayName(__vy_tm_wday(t)), 0, 3) }
    if chunk == "01" { return __vy_pad2(__vy_tm_mon(t)) }
    if chunk == "1" { return str(__vy_tm_mon(t)) }
    if chunk == "02" { return __vy_pad2(__vy_tm_mday(t)) }
    if chunk == "2" { return str(__vy_tm_mday(t)) }
    if chunk == "15" { return __vy_pad2(__vy_tm_hour(t)) }
    if chunk == "03" { return __vy_pad2(__vy_hour12(t)) }
    if chunk == "3" { return str(__vy_hour12(t)) }
    if chunk == "04" { return __vy_pad2(__vy_tm_min(t)) }
    if chunk == "4" { return str(__vy_tm_min(t)) }
    if chunk == "05" { return __vy_pad2(__vy_tm_sec(t)) }
    if chunk == "5" { return str(__vy_tm_sec(t)) }
    if chunk == "PM" {
        if __vy_tm_hour(t) < 12 { return "AM" }
        return "PM"
    }
    if chunk == "pm" {
        if __vy_tm_hour(t) < 12 { return "am" }
        return "pm"
    }
    if chunk == "-07:00" || chunk == "Z07:00" || chunk == "-0700" {
        return __vy_zoneText(t, chunk)
    }
    return chunk
}

fn __vy_hour12(t: int) -> int {
    let h = __vy_tm_hour(t) % 12
    if h == 0 { return 12 }
    return h
}

// The offset from UTC, worked out rather than asked for: the difference
// between the local calendar and the same instant read as a calendar
// gives the zone, and mktime of the local fields gives it back in
// seconds. That avoids needing a second operating system call whose
// answer would have to agree with the first one.
fn __vy_zoneOffset(t: int) -> int {
    let back = __mktime(__vy_tm_year(t), __vy_tm_mon(t), __vy_tm_mday(t),
                        __vy_tm_hour(t), __vy_tm_min(t), __vy_tm_sec(t))
    if back < 0 { return 0 }
    return t - back
}

fn __vy_zoneText(t: int, chunk: str) -> str {
    let off = __vy_zoneOffset(t)
    let sign = "+"
    if off < 0 {
        sign = "-"
        off = -off
    }
    let hh = __vy_pad2(off / 3600)
    let mm = __vy_pad2((off % 3600) / 60)
    if chunk == "-0700" { return sign + hh + mm }
    if chunk == "Z07:00" && off == 0 { return "Z" }
    return sign + hh + ":" + mm
}

fn __vy_timeFormat(t: int, f: str) -> str {
    let layout = __vy_goLayout(f)
    let chunks = __vy_layoutChunks()

    let out = ""
    let i = 0
    while i < len(layout) {
        let matched = false
        let k = 0
        while k < len(chunks) && !matched {
            let pat = chunks[k]
            if i + len(pat) <= len(layout) && __substrB(layout, i, i + len(pat)) == pat {
                out = out + __vy_renderChunk(pat, t)
                i = i + len(pat)
                matched = true
            }
            k = k + 1
        }
        if !matched {
            out = out + __charAtB(layout, i)
            i = i + 1
        }
    }
    return out
}

// Parsing is the same walk with the roles swapped: a chunk consumes as
// much of the text as it needs, anything else has to match literally,
// and any mismatch anywhere gives -1. The Go backend turns every parse
// error into -1 too, so the reasons never have to agree.
fn __vy_timeParse(text: str, f: str) -> int {
    let layout = __vy_goLayout(f)
    let chunks = __vy_layoutChunks()

    let year = 1
    let mon = 1
    let day = 1
    let hour = 0
    let minute = 0
    let sec = 0
    let pm = -1

    let li = 0
    let ti = 0
    while li < len(layout) {
        let matched = false
        let k = 0
        while k < len(chunks) && !matched {
            let pat = chunks[k]
            if li + len(pat) <= len(layout) && __substrB(layout, li, li + len(pat)) == pat {
                matched = true
                li = li + len(pat)

                if pat == "2006" {
                    let v = __vy_takeDigits(text, ti, 4, 4)
                    if v < 0 { return -1 }
                    year = v
                    ti = ti + 4
                } else {
                    if pat == "06" {
                        let v2 = __vy_takeDigits(text, ti, 2, 2)
                        if v2 < 0 { return -1 }
                        year = 2000 + v2
                        if v2 >= 69 { year = 1900 + v2 }
                        ti = ti + 2
                    } else {
                        if pat == "January" || pat == "Jan" {
                            let m = __vy_takeMonthName(text, ti, pat)
                            if m < 0 { return -1 }
                            mon = m
                            ti = ti + len(__vy_monthTextAt(mon, pat))
                        } else {
                            if pat == "Monday" || pat == "Mon" {
                                let d = __vy_takeDayName(text, ti, pat)
                                if d < 0 { return -1 }
                                ti = ti + len(__vy_dayTextAt(d, pat))
                            } else {
                                let packed = __vy_takeNumeric(text, ti, pat)
                                if packed < 0 { return -1 }
                                let taken = packed / 1000
                                let v3 = packed % 1000
                                if pat == "01" || pat == "1" { mon = v3 }
                                if pat == "02" || pat == "2" { day = v3 }
                                if pat == "15" { hour = v3 }
                                if pat == "03" || pat == "3" { hour = v3 }
                                if pat == "04" || pat == "4" { minute = v3 }
                                if pat == "05" || pat == "5" { sec = v3 }
                                if pat == "PM" || pat == "pm" { pm = v3 }
                                ti = ti + taken
                            }
                        }
                    }
                }
            }
            k = k + 1
        }
        if !matched {
            if ti >= len(text) { return -1 }
            if __charAtB(text, ti) != __charAtB(layout, li) { return -1 }
            li = li + 1
            ti = ti + 1
        }
    }
    if ti != len(text) { return -1 }

    if pm == 1 && hour < 12 { hour = hour + 12 }
    if pm == 0 && hour == 12 { hour = 0 }

    return __mktime(year, mon, day, hour, minute, sec)
}
`

// preludeTimeHelpers is split out only because the chunk splitter in
// prelude.go keys on a function starting a line, and these are the
// small pieces the two walks above share.
const preludeTimeHelpers = `
fn __vy_takeDigits(text: str, at: int, least: int, most: int) -> int {
    let n = 0
    let count = 0
    let going = true
    while going && count < most && at + count < len(text) {
        let d = __vy_digitOf(__charAtB(text, at + count))
        if d < 0 {
            going = false
        } else {
            n = n * 10 + d
            count = count + 1
        }
    }
    if count < least { return -1 }
    return n
}

// -1 for anything that is not a digit. Scandiacus has no ordering on strings,
// so a character class is a lookup rather than a range test.
fn __vy_digitOf(c: str) -> int {
    let digits = "0123456789"
    let i = 0
    while i < 10 {
        if __substrB(digits, i, i + 1) == c { return i }
        i = i + 1
    }
    return -1
}

// A numeric or AM/PM chunk, returning the width it used and the value it
// read packed into one int: width * 1000 + value. Scandiacus has no multiple
// return, and every value this reads is below a thousand.
fn __vy_takeNumeric(text: str, at: int, pat: str) -> int {
    if pat == "PM" || pat == "pm" {
        if at + 2 > len(text) { return -1 }
        let two = upper(__substrB(text, at, at + 2))
        if two == "AM" { return 2000 }
        if two == "PM" { return 2001 }
        return -1
    }
    // A two-character chunk is zero padded and takes exactly two; a
    // one-character chunk takes one or two, which is what makes "1"
    // read both 3 and 12.
    let wide = len(pat) == 2
    let least = 1
    if wide { least = 2 }
    let v = __vy_takeDigits(text, at, least, 2)
    if v < 0 { return -1 }
    let used = 1
    if v >= 10 || wide { used = 2 }
    return used * 1000 + v
}

fn __vy_monthTextAt(m: int, pat: str) -> str {
    if pat == "Jan" { return __substrB(__vy_monthName(m), 0, 3) }
    return __vy_monthName(m)
}

fn __vy_dayTextAt(d: int, pat: str) -> str {
    if pat == "Mon" { return __substrB(__vy_dayName(d), 0, 3) }
    return __vy_dayName(d)
}

fn __vy_takeMonthName(text: str, at: int, pat: str) -> int {
    let m = 1
    while m <= 12 {
        let want = __vy_monthTextAt(m, pat)
        if at + len(want) <= len(text) && __substrB(text, at, at + len(want)) == want {
            return m
        }
        m = m + 1
    }
    return -1
}

fn __vy_takeDayName(text: str, at: int, pat: str) -> int {
    let d = 0
    while d <= 6 {
        let want = __vy_dayTextAt(d, pat)
        if at + len(want) <= len(text) && __substrB(text, at, at + len(want)) == want {
            return d
        }
        d = d + 1
    }
    return -1
}

// The rest of the library, all of it the calendar of "now".

fn __vy_timeDate() -> str {
    return __vy_timeFormat(time.now(), "YYYY-MM-DD")
}

fn __vy_timeClock() -> str {
    return __vy_timeFormat(time.now(), "HH:mm:ss")
}

fn __vy_timeStamp() -> str {
    return __vy_timeFormat(time.now(), "YYYY-MM-DD HH:mm:ss")
}

fn __vy_timeYear() -> int { return __vy_tm_year(time.now()) }
fn __vy_timeMonth() -> int { return __vy_tm_mon(time.now()) }
fn __vy_timeDay() -> int { return __vy_tm_mday(time.now()) }
fn __vy_timeWeekday() -> str { return __vy_dayName(__vy_tm_wday(time.now())) }
fn __vy_timeSince(t: int) -> int { return time.now() - t }
`
