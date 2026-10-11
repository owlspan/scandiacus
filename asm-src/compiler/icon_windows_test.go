package main

import (
	"syscall"
	"unsafe"
)

// shellIconCount is how many icons Windows itself finds in a file.
func shellIconCount(path string) int {
	extract := syscall.NewLazyDLL("shell32.dll").NewProc("ExtractIconExW")
	name, _ := syscall.UTF16PtrFromString(path)
	n, _, _ := extract.Call(uintptr(unsafe.Pointer(name)), ^uintptr(0), 0, 0, 0)
	return int(n)
}
