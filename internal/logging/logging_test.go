package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryKeepsRecentLinesAndSplitsPartials(t *testing.T) {
	l := New()
	l.max = 3
	l.Write([]byte("one\ntwo\npartial"))
	// The unfinished line is still visible, so nothing typed is lost mid-write.
	if got := l.Lines(); len(got) != 3 || got[2] != "partial" {
		t.Fatalf("Lines = %q", got)
	}
	l.Write([]byte(" line\nthree\nfour\n"))
	got := l.Lines()
	want := []string{"partial line", "three", "four"} // only the newest three survive
	if len(got) != len(want) {
		t.Fatalf("Lines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Lines = %q, want %q", got, want)
		}
	}
}

func TestOpenWritesAndRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "go-nx.log")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	l.Close()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("file = %q, %v", data, err)
	}

	if err := os.WriteFile(path, make([]byte, maxLogFileBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l2.Close()
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("rotated log should start empty: %v, %v", info, err)
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != maxLogFileBytes {
		t.Fatalf("backup log = %v, %v", info, err)
	}
}

func TestFileErrorStillKeepsMemoryLines(t *testing.T) {
	// A path under an existing file cannot be a directory, so Open must fail.
	base := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(base, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(filepath.Join(base, "go-nx.log"))
	if err == nil {
		t.Fatal("expected Open to fail under a regular file")
	}
	if _, werr := l.Write([]byte("still visible\n")); werr != nil {
		t.Fatalf("Write = %v", werr)
	}
	if got := l.Lines(); len(got) != 1 || !strings.Contains(got[0], "still visible") {
		t.Fatalf("Lines = %q", got)
	}
	if l.FileError() == nil {
		t.Fatal("FileError should report why the file sink is unavailable")
	}
}
