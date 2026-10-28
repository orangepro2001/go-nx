// Command mkzip writes a zip file whose bytes depend only on the files put into it, so
// that the same sources give the same archive on any machine and any day: entries are
// sorted by name, every entry carries the same modification time, permissions are fixed,
// and nothing about the machine or the user is recorded. It is how the core programs are
// packed for release (scripts/artifacts.sh core).
//
//	mkzip -o out.zip -epoch 1793000000 build/nx-host.exe=nx-host.exe docs/nx-host.md=docs/nx-host.md
//
// Each argument is path=name: the file to read and the name it has inside the archive.
// A name is a relative path with forward slashes; ".." and absolute names are refused.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("mkzip", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "the zip file to write")
	epoch := fs.Int64("epoch", 0, "modification time of every entry, seconds since 1970 (0 means 2026-01-01)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: mkzip -o out.zip [-epoch N] path=name ...")
		return 2
	}
	var files []File
	for _, a := range fs.Args() {
		src, name, ok := strings.Cut(a, "=")
		if !ok || src == "" || name == "" {
			fmt.Fprintf(stderr, "mkzip: %q is not path=name\n", a)
			return 2
		}
		data, err := os.ReadFile(src)
		if err != nil {
			fmt.Fprintf(stderr, "mkzip: %v\n", err)
			return 1
		}
		files = append(files, File{Name: name, Data: data})
	}
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if *epoch != 0 {
		mod = time.Unix(*epoch, 0).UTC()
	}
	b, err := Build(files, mod)
	if err != nil {
		fmt.Fprintf(stderr, "mkzip: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintf(stderr, "mkzip: %v\n", err)
		return 1
	}
	return 0
}

// File is one entry of the archive.
type File struct {
	Name string
	Data []byte
}

// Build returns the archive. The same files and time always give the same bytes.
func Build(files []File, mod time.Time) ([]byte, error) {
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	seen := map[string]bool{}
	for _, f := range sorted {
		if err := checkName(f.Name); err != nil {
			return nil, err
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("%q is in the archive twice", f.Name)
		}
		seen[f.Name] = true
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range sorted {
		h := &zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: mod}
		if strings.HasSuffix(f.Name, ".exe") {
			h.SetMode(0o755)
		} else {
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func checkName(name string) error {
	switch {
	case name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\"):
		return fmt.Errorf("%q is not a relative name with forward slashes", name)
	case path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") || strings.HasSuffix(name, "/"):
		return fmt.Errorf("%q is not a clean relative file name", name)
	}
	return nil
}
