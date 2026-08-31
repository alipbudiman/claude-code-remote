//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

// installConsoleHandler registers a native Windows console-control handler
// that runs fn when the console window is closed (CTRL_CLOSE_EVENT — the X
// button on a console-attached server) or the session ends (CTRL_LOGOFF_EVENT
// / CTRL_SHUTDOWN_EVENT).
//
// Go's signal.Notify NEVER receives these three events, so without this
// handler closing the console window kills the server with nothing persisted
// and nothing restarting it. The OS gives the handler very limited time
// (~5s on CTRL_CLOSE_EVENT) before terminating the process regardless — which
// is why fn runs a BOUNDED graceful path, and why M2's per-event durable log
// (not this shutdown path) is the real durability story.
//
// Returning 1 (handled) claims the close/logoff/shutdown events; returning 0
// for everything else lets Go's own runtime console handler run, so Ctrl+C
// still flows through signal.Notify as before.
func installConsoleHandler(fn func()) error {
	handler := windows.NewCallback(func(ctrlType uint32) uintptr {
		switch ctrlType {
		case windows.CTRL_CLOSE_EVENT, windows.CTRL_LOGOFF_EVENT, windows.CTRL_SHUTDOWN_EVENT:
			fn()
			return 1
		}
		return 0
	})

	r1, _, callErr := procSetConsoleCtrlHandler.Call(handler, 1)
	if r1 == 0 {
		return callErr
	}
	return nil
}
