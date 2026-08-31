package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M3 3.4: an existing file over the size cap is rotated to <path>.1 and the
// fresh file starts empty.
func TestOpenLogFileRotatesOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")

	old := strings.Repeat("x", 64)
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := openLogFile(path, 32) // cap far below the existing size
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	if string(rotated) != old {
		t.Fatalf("rotated content = %q, want the old bytes", string(rotated))
	}

	if st, err := f.Stat(); err != nil {
		t.Fatalf("stat fresh log: %v", err)
	} else if st.Size() != 0 {
		t.Fatalf("fresh log size = %d, want 0", st.Size())
	}
	f.Close()
}

// M3 3.4: a file at or under the cap is appended in place, not rotated.
func TestOpenLogFileKeepsSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")

	if err := os.WriteFile(path, []byte("tiny"), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := openLogFile(path, 1024)
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	f.Close()

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("unexpected .1 rotation for a small file (stat err = %v)", err)
	}
	kept, _ := os.ReadFile(path)
	if string(kept) != "tiny" {
		t.Fatalf("content = %q, want %q (appended in place)", string(kept), "tiny")
	}
}

// M3 3.4: missing parent directories are created.
func TestOpenLogFileCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "server.log")

	f, err := openLogFile(path, 1024)
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	f.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
}
