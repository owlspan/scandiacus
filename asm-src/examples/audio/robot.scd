// A voice changer: the microphone, through an effect, to the speakers.
//
//   scandiacus run robot.scd                       a robot voice for ten seconds
//   scandiacus run robot.scd -- echo               an echo instead
//   scandiacus run robot.scd -- --in facecam --out cable
//                                           any part of a device's name
//
// To be heard in Discord or a game, send the output to a virtual audio
// cable (VB-Audio Cable is free) with --out cable, and pick the cable as
// the microphone there. check.scd lists the devices.

let rate = 48000
let frames = 480            // 10 ms blocks: a small delay, a smooth sound

let mic = must(audio.input(rate, 1, frames, args.value("in", "")))
let out = must(audio.output(rate, 1, frames, args.value("out", "")))

let mode = "robot"
if contains(args.rest(), "echo") {
    mode = "echo"
}

// The robot is ring modulation: the voice times a 60 Hz sine.
let phase = 0.0
let turn = 2.0 * PI * 60.0 / float(rate)

// The echo is a quarter of a second ago, at half the volume.
let delay: []float = []
for i in 0..rate / 4 {
    push(delay, 0.0)
}
let at = 0

print("{mode} on for ten seconds - speak")
let blocks = 10 * rate / frames
for b in 0..blocks {
    let block = audio.read(mic)
    let changed: []float = []
    for v in block {
        if mode == "robot" {
            push(changed, v * sin(phase) * 2.0)
            phase += turn
        } else {
            let past = delay[at]
            delay[at] = v + past * 0.5
            push(changed, v + past * 0.5)
            at = (at + 1) % len(delay)
        }
    }
    audio.write(out, changed)
}
audio.close(mic)
audio.close(out)
