//go:build !windows

package main

// shellIconCount is only asked on Windows; see icon_windows_test.go.
func shellIconCount(path string) int { return 1 }
