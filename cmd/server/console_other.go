//go:build !windows

package main

// installConsoleHandler is a no-op on non-Windows platforms: the
// console-close/logoff/shutdown events it handles are Windows-specific, and
// CI cross-compiles linux/darwin where signal.Notify already covers
// termination. The stub keeps main.go buildable everywhere.
func installConsoleHandler(fn func()) error {
	return nil
}
