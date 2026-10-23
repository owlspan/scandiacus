// The os library: files and the environment.
//
// Every call that can fail hands back a T!, so nothing here can go wrong
// quietly. The failure text is the same text the Go backend prints,
// which is why these are written against Win32 rather than the C
// runtime: Go's message for a missing file is FormatMessage's sentence,
// and strerror's is a different one.

let dir = os.env.get("TEMP")
let path = dir + "/scandiacus_files_demo.txt"

print(isOk(os.file.write(path, "alpha\nbeta\n")))
print(os.file.exists(path))
print(must(os.file.size(path)))
print(must(os.file.read(path)))

// Appending opens at the end. Getting that wrong looks like a working
// append until the second call.
print(isOk(os.file.append(path, "gamma\n")))
print(str(must(os.file.lines(path))))

// A file with no trailing newline, and one with none of anything.
print(isOk(os.file.write(path, "single")))
print(str(must(os.file.lines(path))))
print(isOk(os.file.write(path, "")))
print(str(must(os.file.lines(path))))
print(must(os.file.size(path)))

// Renaming, then reading through the new name.
let moved = dir + "/scandiacus_files_demo2.txt"
print(isOk(os.file.write(path, "moved")))
print(isOk(os.file.rename(path, moved)))
print(os.file.exists(path))
print(must(os.file.read(moved)))

// Directories are told apart from files without opening either.
print(os.dir.is(dir))
print(os.dir.is(moved))

// A failure carries a reason rather than stopping the program.
let missing = os.file.read(dir + "/scandiacus_no_such_file.txt")
print(isOk(missing))
print(failed(missing))
print(valueOr(missing, "fell back"))

// The environment. Setting has to be visible to a later get, which is
// the whole reason this one goes through the C runtime.
print(isOk(os.env.set("SCANDIACUS_FILES_DEMO", "set from scandiacus")))
print(os.env.get("SCANDIACUS_FILES_DEMO"))
print(os.env.has("SCANDIACUS_FILES_DEMO"))
print(os.env.has("SCANDIACUS_DEFINITELY_UNSET"))

print(isOk(os.file.delete(moved)))
print(os.file.exists(moved))
