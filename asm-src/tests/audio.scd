// The audio library's checks, which need no sound device: a stream that
// cannot be opened says why, and a closed or bad handle reads nothing,
// writes nothing, and is safe to close.
print(errorOf(audio.input(48000, 3, 960)))
print(errorOf(audio.output(100, 1, 960)))
print(errorOf(audio.output(48000, 1, 4)))
print(len(audio.read(0)))
print(audio.write(0, [0.5, -0.5]))
audio.close(0)
print("done")
