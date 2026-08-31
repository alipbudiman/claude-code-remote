package main

import (
	"os"
	"path/filepath"
)

// maxLogFileSize is the size cap that triggers rotation when the log file is
// opened: an existing file larger than this is renamed to <path>.1 (the
// previous .1 is overwritten) and a fresh file is started.
const maxLogFileSize = 5 * 1024 * 1024 // 5 MB

// openLogFile opens path for appending, creating parent directories as
// needed. If the existing file exceeds sizeCap it is first rotated to
// path+".1" (replacing any earlier rotation) so the new file starts fresh.
func openLogFile(path string, sizeCap int64) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	if st, err := os.Stat(path); err == nil && st.Size() > sizeCap {
		_ = os.Remove(path + ".1") // replace any earlier rotation...
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err // ...but never lose the live log to a rename race
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}
