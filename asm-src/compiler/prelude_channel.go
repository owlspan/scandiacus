package main

// Channel<T>: a queue between threads, in Scandiacus, on the mutex and
// condition variable threads.go provides.
//
//	let ch = channel<int>()
//	thread.spawn(fn() {
//	    ch.send(42)
//	    ch.close()
//	})
//	let v = ch.recv()      // ?int: 42, then nil once closed and empty
//
// A struct is copied when it is passed or stored, so nothing a copy
// could get out of step on lives in the struct itself: the values are
// in a list, which copies share, and where reading has got to, whether
// it is closed, the mutex and the condition variable are in a block of
// native memory the struct holds the address of.
//
// It is only compiled into a program that mentions a channel, and is
// generic, so each element type a program uses is its own copy.

import "sync"

// The state block: four words.
const channelSource = `
struct Channel<T> {
    items: []T
    state: int
}

fn channel<T>() -> Channel<T> {
    let st = mem.alloc(32)
    mem.write64(st + 16, thread.mutex())
    mem.write64(st + 24, thread.cond())
    return Channel<T>{state: st}
}

impl Channel<T> {
    // Adds a value, and wakes whoever is waiting for one.
    fn send(self, v: T) {
        let m = mem.readI64(self.state + 16)
        thread.lock(m)
        if mem.readI64(self.state + 8) != 0 {
            thread.unlock(m)
            __abortStr("send on a closed channel")
        }
        push(self.items, v)
        thread.notifyAll(mem.readI64(self.state + 24))
        thread.unlock(m)
    }

    // The next value, waiting for one if there is none yet; nil once
    // the channel is closed and every value has been taken.
    fn recv(self) -> ?T {
        let m = mem.readI64(self.state + 16)
        thread.lock(m)
        while mem.readI64(self.state) >= len(self.items) && mem.readI64(self.state + 8) == 0 {
            thread.wait(mem.readI64(self.state + 24), m)
        }
        let head = mem.readI64(self.state)
        if head >= len(self.items) {
            thread.unlock(m)
            return nil
        }
        let v = self.items[head]
        head += 1
        // What has been read is dropped from the front now and then, in
        // place, so a long-lived channel does not keep every value.
        if head >= 64 && head * 2 >= len(self.items) {
            let n = len(self.items) - head
            let i = 0
            while i < n {
                self.items[i] = self.items[head + i]
                i += 1
            }
            while len(self.items) > n {
                pop(self.items)
            }
            head = 0
        }
        mem.write64(self.state, head)
        thread.unlock(m)
        return v
    }

    // No more values will be sent. Waiting receivers wake and get nil
    // once what is left has been taken.
    fn close(self) {
        let m = mem.readI64(self.state + 16)
        thread.lock(m)
        mem.write64(self.state + 8, 1)
        thread.notifyAll(mem.readI64(self.state + 24))
        thread.unlock(m)
    }

    // How many values are waiting to be received.
    fn pending(self) -> int {
        let m = mem.readI64(self.state + 16)
        thread.lock(m)
        let n = len(self.items) - mem.readI64(self.state)
        thread.unlock(m)
        return n
    }
}
`

// Builder puts a string together from pieces in time proportional to
// its length. `s = s + piece` in a loop copies all of s each time, which
// is quadratic; a Builder keeps the pieces and joins them once, when the
// string is asked for, and keeps the result as its one piece so asking
// again is free.
//
//	let sb = Builder{}
//	for i in 0..1000 {
//	    sb.add("{i},")
//	}
//	print(sb.str())
const builderSource = `
struct Builder {
    parts: []str
}

impl Builder {
    fn add(self, s: str) {
        push(self.parts, s)
    }

    fn addLine(self, s: str) {
        push(self.parts, s)
        push(self.parts, "\n")
    }

    fn str(self) -> str {
        let n = len(self.parts)
        if n == 0 {
            return ""
        }
        if n == 1 {
            return self.parts[0]
        }
        let s = join(self.parts, "")
        while len(self.parts) > 0 {
            pop(self.parts)
        }
        push(self.parts, s)
        return s
    }

    fn len(self) -> int {
        return len(self.str())
    }

    fn clear(self) {
        while len(self.parts) > 0 {
            pop(self.parts)
        }
    }
}
`

var (
	builderOnce sync.Once
	builderProg *Program
	builderErrs []string
)

var (
	channelOnce sync.Once
	channelProg *Program
	channelErrs []string
)

// addLibrary folds in the library types written in Scandiacus that the
// program names: Channel<T> and Builder.
func addLibrary(prog *Program, sources []string) []string {
	if errs := addChannel(prog, sources); len(errs) > 0 {
		return errs
	}
	return addBuilder(prog, sources)
}

func addBuilder(prog *Program, sources []string) []string {
	named := false
	for _, src := range sources {
		if mentions(src, "Builder") {
			named = true
			break
		}
	}
	if !named {
		return nil
	}
	builderOnce.Do(func() {
		lx := NewLexer("<prelude>", builderSource)
		ps := NewParser("<prelude>", lx.Scan())
		builderProg = ps.ParseProgram()
		builderErrs = append(append([]string{}, lx.Errors...), ps.Errors...)
	})
	if len(builderErrs) > 0 {
		return builderErrs
	}
	for _, d := range builderProg.Structs {
		d.File = "<prelude>"
		prog.Structs = append(prog.Structs, d)
	}
	for _, f := range builderProg.Funcs {
		f.File = "<prelude>"
		prog.Funcs = append(prog.Funcs, f)
	}
	return nil
}

// addChannel folds Channel<T> into a program that mentions one.
func addChannel(prog *Program, sources []string) []string {
	named := false
	for _, src := range sources {
		if mentions(src, "channel") || mentions(src, "Channel") {
			named = true
			break
		}
	}
	if !named {
		return nil
	}
	channelOnce.Do(func() {
		lx := NewLexer("<prelude>", channelSource)
		ps := NewParser("<prelude>", lx.Scan())
		channelProg = ps.ParseProgram()
		channelErrs = append(append([]string{}, lx.Errors...), ps.Errors...)
	})
	if len(channelErrs) > 0 {
		return channelErrs
	}
	for _, d := range channelProg.Structs {
		d.File = "<prelude>"
		prog.Structs = append(prog.Structs, d)
	}
	for _, f := range channelProg.Funcs {
		f.File = "<prelude>"
		prog.Funcs = append(prog.Funcs, f)
	}
	return nil
}
