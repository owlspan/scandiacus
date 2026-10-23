// Paths, text replacement, and asking the machine about itself.
//
// The path helpers never touch the disk - they are string work, and a
// separator is either slash, because Windows accepts both and Scandiacus
// programs are written with forward ones.

// Separators are normalised on the way out so this prints the same
// whatever join produced.
print(replace(os.path.join("a", "b", "c.txt"), "\\", "/"))
print(replace(os.path.dir("/x/y/z.txt"), "\\", "/"))
print(os.path.base("/x/y/z.txt"))
print(os.path.ext("/x/y/z.txt"))
print(os.path.ext("/x/y/noext"))
print(os.path.base("bare.txt"))
print(replace(os.path.dir("bare.txt"), "\\", "/"))

// A dot in a directory name is not an extension: the dot is looked for
// in the base, not in the whole path.
print(os.path.ext("/x.y/z"))

// replace is every occurrence, left to right.
print(replace("a-b-c", "-", "+"))
print(replace("aaa", "aa", "b"))
print(replace("nothing here", "zz", "!"))
print(replace("", "a", "b"))

// The machine. Nothing here has a value worth printing - it differs
// between machines - so the program checks the shape of the answer.
print(os.cpus() > 0)
print(os.pid() > 0)
print(len(os.hostname()) > 0)

// Running a program. A command that does not exist fails rather than
// returning empty output.
print(isOk(os.run("definitely_not_a_program_xyz", [])))

// readOr is read with a fallback, so it never has to be unwrapped.
print(os.file.readOr("no_such_file_xyz.txt", "fallback"))

// The older spellings of the file functions are the same functions.
let p = os.env.get("TEMP") + "/scandiacus_paths_demo.txt"
print(isOk(os.write.file(p, "written the old way")))
print(must(os.read.file(p)))
print(isOk(os.delete.file(p)))
