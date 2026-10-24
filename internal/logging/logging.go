// Package logging provides a bounded in-memory log view and a rotating file sink.
package logging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const (
	defaultLines    = 2000
	maxLineBytes    = 4096
	maxLogFileBytes = 2 << 20
)

// Logger is safe for concurrent writes. It always retains recent log lines in
// memory, even if the optional file sink becomes unavailable.
type Logger struct {
	mu      sync.Mutex
	file    *os.File
	lines   []string
	pending []byte
	max     int
	rev     uint64
	fileErr error
}

func New() *Logger { return &Logger{max: defaultLines} }

// Open creates or appends to path, rotating an oversized log to path + ".1".
// It returns a usable in-memory logger together with any file setup error.
func Open(path string) (*Logger, error) {
	l := New()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		l.fileErr = err
		return l, err
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.Size() >= maxLogFileBytes:
		backup := path + ".1"
		if removeErr := os.Remove(backup); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			l.fileErr = removeErr
			return l, removeErr
		}
		if err := os.Rename(path, backup); err != nil {
			l.fileErr = err
			return l, err
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		l.fileErr = err
		return l, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		l.fileErr = err
		return l, err
	}
	l.file = f
	return l, nil
}

func (l *Logger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		if _, err := l.file.Write(p); err != nil {
			l.fileErr = err
			_ = l.file.Close()
			l.file = nil
		}
	}
	l.pending = append(l.pending, p...)
	for {
		i := bytes.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		l.addLine(string(l.pending[:i]))
		l.pending = append(l.pending[:0], l.pending[i+1:]...)
	}
	if len(l.pending) > maxLineBytes {
		l.addLine(string(l.pending[:maxLineBytes]) + " …[line truncated]")
		l.pending = append(l.pending[:0], l.pending[maxLineBytes:]...)
	}
	l.rev++
	return len(p), nil // retain logs in memory even if disk writes fail
}

func (l *Logger) addLine(line string) {
	if i := bytes.LastIndexByte([]byte(line), '\r'); i >= 0 && i == len(line)-1 {
		line = line[:i]
	}
	if len(line) > maxLineBytes {
		line = line[:maxLineBytes] + " …[line truncated]"
	}
	l.lines = append(l.lines, line)
	if len(l.lines) > l.max {
		l.lines = append(l.lines[:0], l.lines[len(l.lines)-l.max:]...)
	}
}

// Lines returns a snapshot of recent complete lines, plus any partial line.
func (l *Logger) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	lines := append([]string(nil), l.lines...)
	if len(l.pending) > 0 {
		lines = append(lines, string(l.pending))
	}
	return lines
}

// Revision changes whenever new log data arrives, so views can cache snapshots.
func (l *Logger) Revision() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rev
}

func (l *Logger) FileError() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fileErr
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return l.fileErr
	}
	if err := l.file.Close(); err != nil {
		l.fileErr = err
		l.file = nil
		return err
	}
	l.file = nil
	return nil
}
