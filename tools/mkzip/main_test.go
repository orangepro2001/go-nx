package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 4, 5, 6, 8, 0, time.UTC)

func files() []File {
	return []File{{"nx-host.exe", []byte("MZ host")}, {"docs/nx-host.md", []byte("# contract\n")}, {"SHA256SUMS", []byte("abc  nx-host.exe\n")}}
}

func TestTheSameFilesGiveTheSameBytesWhateverTheOrder(t *testing.T) {
	a, err := Build(files(), t0)
	if err != nil {
		t.Fatal(err)
	}
	rev := files()
	rev[0], rev[2] = rev[2], rev[0]
	b, err := Build(rev, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("the order the files were given in changed the archive")
	}
	c, _ := Build(files(), t0.Add(24*time.Hour))
	if bytes.Equal(a, c) {
		t.Fatal("the modification time is not in the archive")
	}
}

func TestTheArchiveHoldsWhatWasPutIn(t *testing.T) {
	b, err := Build(files(), t0)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		var want []byte
		for _, in := range files() {
			if in.Name == f.Name {
				want = in.Data
			}
		}
		if !bytes.Equal(data, want) {
			t.Errorf("%s holds %q, want %q", f.Name, data, want)
		}
		if !f.Modified.Equal(t0) {
			t.Errorf("%s modified %v, want %v", f.Name, f.Modified, t0)
		}
		if strings.HasSuffix(f.Name, ".exe") != (f.Mode()&0o111 != 0) {
			t.Errorf("%s has mode %v", f.Name, f.Mode())
		}
	}
	if got := strings.Join(names, ","); got != "SHA256SUMS,docs/nx-host.md,nx-host.exe" {
		t.Errorf("entries = %s, want them sorted", got)
	}
}

func TestBadNamesAndDuplicatesAreRefused(t *testing.T) {
	for _, name := range []string{"", "/etc/passwd", "../x", "a/../../x", "a\\b", "dir/", ".", "a//b"} {
		if _, err := Build([]File{{name, []byte("x")}}, t0); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
	if _, err := Build([]File{{"a", nil}, {"a", nil}}, t0); err == nil {
		t.Error("a duplicate name was accepted")
	}
}

func TestCommandLine(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.bin")
	os.WriteFile(src, []byte("payload"), 0o644)
	out := filepath.Join(dir, "o.zip")
	var stderr bytes.Buffer
	if code := run([]string{"-o", out, "-epoch", "1793000000", src + "=x.bin"}, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	first, _ := os.ReadFile(out)
	os.Remove(out)
	if code := run([]string{"-o", out, "-epoch", "1793000000", src + "=x.bin"}, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	second, _ := os.ReadFile(out)
	if !bytes.Equal(first, second) {
		t.Error("two runs gave different archives")
	}
	for _, args := range [][]string{{}, {"-o", out}, {"-o", out, "noequals"}, {"-o", out, "=name"}} {
		if code := run(args, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (usage)", args, code)
		}
	}
	if code := run([]string{"-o", out, filepath.Join(dir, "missing") + "=m"}, &stderr); code != 1 {
		t.Errorf("a missing file gave %d, want 1", code)
	}
}
