// Directories.
//
// Listing, making and removing a tree. All three walk the same Win32
// find record; removing is the only os call here that is a loop rather
// than one syscall, and it is written as a worklist rather than as a
// recursion because the lowerer inlines what it lowers.

let base = os.env.get("TEMP") + "/scandiacus_dirs_demo"

// Removing something that is not there is success, which is what
// os.RemoveAll means. Starting with it makes the example repeatable.
print(isOk(os.dir.delete(base)))

// Every missing parent is created too.
print(isOk(os.dir.make(base + "/deep/inner")))
print(os.dir.is(base))
print(os.dir.is(base + "/deep/inner"))

// Making one that already exists is not an error.
print(isOk(os.dir.make(base)))

print(isOk(os.file.write(base + "/gamma.txt", "g")))
print(isOk(os.file.write(base + "/alpha.txt", "a")))
print(isOk(os.file.write(base + "/deep/note.txt", "n")))

// Sorted, and without "." and "..". Sorted because os.ReadDir sorts,
// and a listing in the file system's own order would agree on most
// directories and quietly disagree on some.
print(str(must(os.dir.list(base))))
print(str(must(os.dir.list(base + "/deep"))))
print(len(must(os.dir.list(base + "/deep/inner"))))

// A directory that is not there cannot be listed, and says so.
print(isOk(os.dir.list(base + "/no_such_place")))

// Counting what is inside, one level down.
let total = 0
for name in must(os.dir.list(base)) {
    if os.dir.is(base + "/" + name) {
        total += len(must(os.dir.list(base + "/" + name)))
    } else {
        total += 1
    }
}
print(total)

// The whole tree, files first and directories back to front.
print(isOk(os.dir.delete(base)))
print(os.file.exists(base))
