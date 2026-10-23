package main

// stats and term, in Scandiacus.
//
// stats is arithmetic over a list of floats, so it is exactly what the
// prelude is for. term is string decoration plus one question the
// operating system answers - whether standard output is a console - and
// that question decides everything else in it.

const preludeStats = `
// Every one of these takes a list of floats. The Go backend widens an
// int list to floats before calling, and the caller here does the same,
// so a mixed or integer list arrives already converted.

fn __vy_mean(xs: []float) -> float {
    if len(xs) == 0 { return 0.0 }
    let total = 0.0
    for x in xs {
        total = total + x
    }
    return total / float(len(xs))
}

fn __vy_median(xs: []float) -> float {
    if len(xs) == 0 { return 0.0 }
    let s = sort(xs)
    let mid = len(s) / 2
    if len(s) % 2 == 1 { return s[mid] }
    return (s[mid - 1] + s[mid]) / 2.0
}

// The sample variance, dividing by n-1. One value has no spread to
// measure and gives zero rather than a division by zero.
fn __vy_variance(xs: []float) -> float {
    if len(xs) < 2 { return 0.0 }
    let m = __vy_mean(xs)
    let total = 0.0
    for x in xs {
        total = total + (x - m) * (x - m)
    }
    return total / float(len(xs) - 1)
}

fn __vy_stdev(xs: []float) -> float {
    return sqrt(__vy_variance(xs))
}

// Linear interpolation between the two neighbouring ranks, which is what
// spreadsheets and numpy both do by default.
fn __vy_percentile(xs: []float, p: float) -> float {
    if len(xs) == 0 { return 0.0 }
    let s = sort(xs)
    if p <= 0.0 { return s[0] }
    if p >= 100.0 { return s[len(s) - 1] }

    let pos = (p / 100.0) * float(len(s) - 1)
    let lo = int(floor(pos))
    let hi = int(ceil(pos))
    if lo == hi { return s[lo] }
    return s[lo] + (pos - float(lo)) * (s[hi] - s[lo])
}
`

const preludeTerm = `
// Colour is emitted only when standard output is a console. A program
// whose output is being captured - which is what the test harness does -
// gets bare text, and that is the behaviour being relied on rather than
// a detail: escape sequences in a captured file would differ from the Go
// backend only in how they look, which is the worst kind of difference.

fn __vy_style(code: str, s: str) -> str {
    if !__vy_termColour() { return s }
    return __chr(27) + "[" + code + "m" + s + __chr(27) + "[0m"
}

fn __vy_termColour() -> bool {
    if os.env.has("NO_COLOR") { return false }
    return __isatty()
}

fn __vy_termRed(s: str) -> str { return __vy_style("31", s) }
fn __vy_termGreen(s: str) -> str { return __vy_style("32", s) }
fn __vy_termYellow(s: str) -> str { return __vy_style("33", s) }
fn __vy_termBlue(s: str) -> str { return __vy_style("34", s) }
fn __vy_termMagenta(s: str) -> str { return __vy_style("35", s) }
fn __vy_termCyan(s: str) -> str { return __vy_style("36", s) }
fn __vy_termGrey(s: str) -> str { return __vy_style("90", s) }
fn __vy_termBold(s: str) -> str { return __vy_style("1", s) }
fn __vy_termDim(s: str) -> str { return __vy_style("2", s) }
fn __vy_termUnderline(s: str) -> str { return __vy_style("4", s) }
fn __vy_termInvert(s: str) -> str { return __vy_style("7", s) }

fn __vy_termClear() {
    write(__chr(27) + "[2J" + __chr(27) + "[H")
}

fn __vy_termBar(done: int, total: int, width: int) -> str {
    if total <= 0 || width <= 0 { return "" }
    let d = done
    if d < 0 { d = 0 }
    if d > total { d = total }
    let filled = d * width / total
    return "[" + repeat("#", filled) + repeat("-", width - filled) + "]"
}
`
