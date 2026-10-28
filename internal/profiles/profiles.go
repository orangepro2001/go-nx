// Package profiles stores the saved connections shown on the home tab.
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/orangepro2001/go-nx/internal/fsutil"
)

// Profile is one saved connection. Password may be empty. It is stored in clear
// text in the file, which is only readable by the current user.
type Profile struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Password string `json:"password,omitempty"`
	// LastUsed is when this connection was last opened, in seconds since 1970; 0 for
	// never. Pinned connections are listed first. Both are older files' missing fields.
	LastUsed int64 `json:"lastUsed,omitempty"`
	Pinned   bool  `json:"pinned,omitempty"`
}

// List is the ordered set of saved profiles, persisted to a JSON file.
type List struct {
	path  string
	Items []Profile
}

// Load reads path, or starts empty if it does not exist.
func Load(path string) (*List, error) {
	l := &List{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &l.Items); err != nil {
		return nil, err
	}
	return l, nil
}

// LoadRecovering is Load for start-up: a file that exists but cannot be parsed
// is moved aside (its new name is returned as backup) and the list starts empty.
// The caller should tell the user, since the saved connections in it are no
// longer loaded.
func LoadRecovering(path string) (l *List, backup string, err error) {
	l, err = Load(path)
	if err == nil {
		return l, "", nil
	}
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	if !errors.As(err, &syn) && !errors.As(err, &typ) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, "", err
	}
	if backup, err = fsutil.Quarantine(path); err != nil {
		return nil, "", err
	}
	return &List{path: path}, backup, nil
}

func (l *List) save() error {
	b, err := json.MarshalIndent(l.Items, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(l.path, b, 0o600)
}

func (l *List) index(name string) int {
	for i, p := range l.Items {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// Find returns the profile called name.
func (l *List) Find(name string) (Profile, bool) {
	if i := l.index(name); i >= 0 {
		return l.Items[i], true
	}
	return Profile{}, false
}

// Upsert replaces the profile with the same name, or appends a new one.
func (l *List) Upsert(p Profile) error {
	if i := l.index(p.Name); i >= 0 {
		l.Items[i] = p
	} else {
		l.Items = append(l.Items, p)
	}
	return l.save()
}

// Delete removes the profile at index i.
func (l *List) Delete(i int) error {
	if i < 0 || i >= len(l.Items) {
		return nil
	}
	l.Items = append(l.Items[:i], l.Items[i+1:]...)
	return l.save()
}

// Rename renames the profile called old and returns the name actually used: if
// another profile already has the name, " (2)", " (3)", ... is appended. Renaming
// a profile that does not exist, or to an empty name, changes nothing.
func (l *List) Rename(old, name string) (string, error) {
	i := l.index(old)
	if i < 0 || name == "" || name == old {
		return old, nil
	}
	final := name
	for n := 2; l.index(final) >= 0; n++ {
		final = fmt.Sprintf("%s (%d)", name, n)
	}
	l.Items[i].Name = final
	return final, l.save()
}

// Touch records that the connection called name was just opened.
func (l *List) Touch(name string, now time.Time) error {
	i := l.index(name)
	if i < 0 {
		return nil
	}
	l.Items[i].LastUsed = now.Unix()
	return l.save()
}

// SetPinned pins or unpins the connection called name.
func (l *List) SetPinned(name string, pinned bool) error {
	i := l.index(name)
	if i < 0 || l.Items[i].Pinned == pinned {
		return nil
	}
	l.Items[i].Pinned = pinned
	return l.save()
}

// Replace puts p in place of the connection called old, which keeps its place in the list.
// If p is named like another connection, " (2)", " (3)", ... is appended to the name; the
// name actually used is returned. Replacing a connection that does not exist adds p.
func (l *List) Replace(old string, p Profile) (string, error) {
	i := l.index(old)
	if i < 0 {
		return p.Name, l.Upsert(p)
	}
	name := p.Name
	for n := 2; ; n++ {
		if j := l.index(name); j < 0 || j == i {
			break
		}
		name = fmt.Sprintf("%s (%d)", p.Name, n)
	}
	p.Name = name
	l.Items[i] = p
	return name, l.save()
}

// All is the saved connections, in the order they are kept.
func (l *List) All() []Profile { return l.Items }
