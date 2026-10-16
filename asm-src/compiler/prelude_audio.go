package main

// Live audio: a stream from the microphone or to the speakers, a block
// of samples at a time.
//
//	let mic = must(audio.input(48000, 1, 960))     // 20 ms blocks
//	let out = must(audio.output(48000, 1, 960))
//	while true {
//	    let block = audio.read(mic)
//	    audio.write(out, block)                     // or change it first
//	}
//
// Samples are floats from -1 to 1, interleaved when there are two
// channels, and go over the wire as 16-bit PCM. read waits for the next
// block and gives frames * channels samples; write queues its samples
// and waits only when every buffer is still playing.
//
// On Windows it is the waveIn and waveOut functions of winmm, polled
// rather than called back: eight buffers a stream, a buffer done when
// its WAVEHDR says so. On Linux it is PulseAudio's simple API, which
// blocks on its own. Both are loaded at the first call, so a program
// that never opens a stream needs neither. macOS is not done yet.
//
// A stream is a block of words from mem.alloc:
//
//	0 kind (1 in, 2 out)   8 the system's handle   16 channels
//	24 frames a block      32 buffers              40 the next buffer
//	48 bytes a buffer      56 sample memory        64 the WAVEHDRs
//	72 buffers written so far (output)             80 a scratch word

const preludeAudio = `
fn __vy_audioOpen(rate: int, channels: int, frames: int, out: bool) -> int! {
    if channels != 1 && channels != 2 {
        return fail("audio: channels must be 1 or 2, got " + str(channels))
    }
    if rate < 8000 || rate > 192000 {
        return fail("audio: a rate of " + str(rate) + " samples a second is not one a device takes")
    }
    if frames < 16 || frames > rate {
        return fail("audio: a block of " + str(frames) + " frames is too small or more than a second")
    }
    let s = mem.alloc(88)
    mem.write64(s, 1)
    if out { mem.write64(s, 2) }
    mem.write64(s + 16, channels)
    mem.write64(s + 24, frames)
    mem.write64(s + 48, frames * channels * 2)
    if os.name() == "windows" {
        return __vy_audioOpenWin(s, rate, out)
    }
    if os.name() == "linux" {
        return __vy_audioOpenPulse(s, rate, out)
    }
    mem.free(s)
    return fail("audio streams are not on " + os.name() + " yet")
}

fn __vy_audioInput(rate: int, channels: int, frames: int) -> int! {
    return __vy_audioOpen(rate, channels, frames, false)
}

fn __vy_audioOutput(rate: int, channels: int, frames: int) -> int! {
    return __vy_audioOpen(rate, channels, frames, true)
}

fn __vy_winmm(name: str) -> int {
    return mem.symbol("winmm.dll", name)
}

fn __vy_audioOpenWin(s: int, rate: int, out: bool) -> int! {
    let ch = mem.readI64(s + 16)
    let fmt = mem.alloc(24)
    mem.write16(fmt, 1)
    mem.write16(fmt + 2, ch)
    mem.write32(fmt + 4, rate)
    mem.write32(fmt + 8, rate * ch * 2)
    mem.write16(fmt + 12, ch * 2)
    mem.write16(fmt + 14, 16)
    let prefix = "waveIn"
    if out { prefix = "waveOut" }
    // WAVE_MAPPER, the device the user chose in Settings; no callback.
    let r = mem.call(__vy_winmm(prefix + "Open"), s + 8, -1, fmt, 0, 0, 0) & 0xFFFFFFFF
    mem.free(fmt)
    if r != 0 {
        mem.free(s)
        let what = "microphone"
        if out { what = "speakers" }
        return fail("audio: the " + what + " could not be opened (error " + str(r) + ")")
    }
    let h = mem.readI64(s + 8)
    // A millisecond timer while the stream is open: the waits below
    // sleep a millisecond at a time, and Windows' default tick is
    // 15.6 ms, longer than a block, which made a stream fall behind.
    mem.call(__vy_winmm("timeBeginPeriod"), 1)
    let nbuf = 8
    let bytes = mem.readI64(s + 48)
    mem.write64(s + 32, nbuf)
    let data = mem.alloc(nbuf * bytes)
    let hdrs = mem.alloc(nbuf * 48)
    mem.write64(s + 56, data)
    mem.write64(s + 64, hdrs)
    let i = 0
    while i < nbuf {
        let w = hdrs + i * 48
        mem.write64(w, data + i * bytes)
        mem.write32(w + 8, bytes)
        mem.call(__vy_winmm(prefix + "PrepareHeader"), h, w, 48)
        if !out {
            mem.call(__vy_winmm("waveInAddBuffer"), h, w, 48)
        }
        i += 1
    }
    if !out {
        mem.call(__vy_winmm("waveInStart"), h)
    }
    return s
}

fn __vy_audioOpenPulse(s: int, rate: int, out: bool) -> int! {
    let lib = "libpulse-simple.so.0"
    let make = mem.symbol(lib, "pa_simple_new")
    if make == 0 {
        mem.free(s)
        return fail("audio needs PulseAudio (libpulse-simple.so.0), which was not found")
    }
    let spec = mem.alloc(16)
    mem.write32(spec, 3)
    mem.write32(spec + 4, rate)
    mem.write8(spec + 8, mem.readI64(s + 16))
    let dir = 2
    if out { dir = 1 }
    // The buffering, as pa_buffer_attr: PulseAudio's own default holds
    // about two seconds, which is no use for anything live. About four
    // blocks queued to play, and a read handed over block by block;
    // -1 is "the server's choice" for the rest.
    let bytes = mem.readI64(s + 48)
    let attr = mem.alloc(24)
    mem.write32(attr, -1)
    mem.write32(attr + 4, bytes * 4)
    mem.write32(attr + 8, -1)
    mem.write32(attr + 12, -1)
    mem.write32(attr + 16, bytes)
    let p = mem.call(make, 0, "veyl", dir, 0, "audio", spec, 0, attr, s + 80)
    mem.free(spec)
    mem.free(attr)
    if p == 0 {
        mem.free(s)
        return fail("audio: PulseAudio would not open a stream (error " + str(mem.readI32(s + 80)) + ")")
    }
    mem.write64(s + 8, p)
    mem.write64(s + 56, mem.alloc(mem.readI64(s + 48)))
    return s
}

// The next block from an input stream.
fn __vy_audioRead(s: int) -> []float {
    let out: []float = []
    if s == 0 || mem.readI64(s) != 1 { return out }
    let bytes = mem.readI64(s + 48)
    let data = mem.readI64(s + 56)
    let got = bytes
    if os.name() == "windows" {
        let i = mem.readI64(s + 40)
        let w = mem.readI64(s + 64) + i * 48
        // WHDR_DONE: the device has filled it.
        while (mem.readU32(w + 24) & 1) == 0 {
            sleep(1)
        }
        got = mem.readU32(w + 12)
        data = mem.readI64(w)
        let k = 0
        while k < got / 2 {
            push(out, float(mem.readI16(data + k * 2)) / 32768.0)
            k += 1
        }
        mem.call(__vy_winmm("waveInAddBuffer"), mem.readI64(s + 8), w, 48)
        mem.write64(s + 40, (i + 1) % mem.readI64(s + 32))
        return out
    }
    let read = mem.symbol("libpulse-simple.so.0", "pa_simple_read")
    if mem.call(read, mem.readI64(s + 8), data, bytes, s + 80) != 0 { return out }
    let k = 0
    while k < bytes / 2 {
        push(out, float(mem.readI16(data + k * 2)) / 32768.0)
        k += 1
    }
    return out
}

// A sample as a 16-bit value, clipped to the range.
fn __vy_audioPCM(v: float) -> int {
    if v >= 1.0 { return 32767 }
    if v <= -1.0 { return -32768 }
    return round(v * 32767.0)
}

// Queue samples on an output stream, a block at a time.
fn __vy_audioWrite(s: int, samples: []float) -> bool {
    if s == 0 || mem.readI64(s) != 2 { return false }
    let bytes = mem.readI64(s + 48)
    let per = bytes / 2
    let at = 0
    while at < len(samples) {
        let n = len(samples) - at
        if n > per { n = per }
        if os.name() == "windows" {
            let i = mem.readI64(s + 40)
            let w = mem.readI64(s + 64) + i * 48
            // A buffer already handed over is waited for until it has
            // played; each is free the first time round.
            if mem.readI64(s + 72) >= mem.readI64(s + 32) {
                while (mem.readU32(w + 24) & 1) == 0 {
                    sleep(1)
                }
            }
            let data = mem.readI64(w)
            let k = 0
            while k < n {
                mem.write16(data + k * 2, __vy_audioPCM(samples[at + k]))
                k += 1
            }
            mem.write32(w + 8, n * 2)
            mem.call(__vy_winmm("waveOutWrite"), mem.readI64(s + 8), w, 48)
            mem.write64(s + 40, (i + 1) % mem.readI64(s + 32))
            mem.write64(s + 72, mem.readI64(s + 72) + 1)
        } else {
            let data = mem.readI64(s + 56)
            let k = 0
            while k < n {
                mem.write16(data + k * 2, __vy_audioPCM(samples[at + k]))
                k += 1
            }
            let write = mem.symbol("libpulse-simple.so.0", "pa_simple_write")
            if mem.call(write, mem.readI64(s + 8), data, n * 2, s + 80) != 0 { return false }
        }
        at += n
    }
    return true
}

// Close a stream. An output stream plays what is queued first.
fn __vy_audioClose(s: int) {
    if s == 0 { return }
    let h = mem.readI64(s + 8)
    let out = mem.readI64(s) == 2
    if os.name() == "windows" {
        let prefix = "waveIn"
        if out { prefix = "waveOut" }
        let nbuf = mem.readI64(s + 32)
        if out {
            // Wait for what was written to finish, a few seconds at most.
            let waited = 0
            let i = 0
            while i < nbuf && i < mem.readI64(s + 72) && waited < 5000 {
                let w = mem.readI64(s + 64) + i * 48
                if (mem.readU32(w + 24) & 1) == 0 {
                    sleep(1)
                    waited += 1
                } else {
                    i += 1
                }
            }
        } else {
            mem.call(__vy_winmm("waveInStop"), h)
        }
        mem.call(__vy_winmm(prefix + "Reset"), h)
        let i = 0
        while i < nbuf {
            mem.call(__vy_winmm(prefix + "UnprepareHeader"), h, mem.readI64(s + 64) + i * 48, 48)
            i += 1
        }
        mem.call(__vy_winmm(prefix + "Close"), h)
        mem.call(__vy_winmm("timeEndPeriod"), 1)
        mem.free(mem.readI64(s + 56))
        mem.free(mem.readI64(s + 64))
        mem.free(s)
        return
    }
    if out {
        mem.call(mem.symbol("libpulse-simple.so.0", "pa_simple_drain"), h, s + 80)
    }
    mem.call(mem.symbol("libpulse-simple.so.0", "pa_simple_free"), h)
    mem.free(mem.readI64(s + 56))
    mem.free(s)
}
`
