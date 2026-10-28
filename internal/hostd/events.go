package hostd

import (
	"encoding/json"
	"errors"
	"strings"
)

// SchemaVersion is the version of the control protocol: the names and meanings of
// the commands and events below. It changes only when a name is removed or a
// meaning changes; adding a command, an event or a field does not change it, so a
// controller ignores fields it does not know. The ready event carries it.
const SchemaVersion = 1

// Event is one line a host writes. Which fields are set depends on Event; see
// docs/nx-host.md. Fields whose zero value means something (a boolean that is
// false, a count that is 0) are pointers so that they can be told from absent.
type Event struct {
	Schema    int    `json:"schema,omitempty"` // ready only
	Event     string `json:"event"`
	ID        string `json:"id,omitempty"` // the id of the command this answers
	Version   string `json:"version,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Listen    string `json:"listen,omitempty"` // the address actually bound
	Display   string `json:"display,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	DPI       int    `json:"dpi,omitempty"`
	Identity  string `json:"identity,omitempty"` // the host's fingerprint; in connected / denied, the viewer's
	Name      string `json:"name,omitempty"`     // the viewer's display name (connected)
	Protocol  *int   `json:"protocol,omitempty"` // control-protocol version the viewer speaks (connected)
	Code      string `json:"code,omitempty"`     // the pairing code (pairing)
	ExpiresIn *int   `json:"expiresIn,omitempty"`
	Addr      string `json:"addr,omitempty"`
	Reason    string `json:"reason,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"` // error only: a stable word
	Message   string `json:"message,omitempty"`   // for people; not to be parsed

	FPS         int      `json:"fps,omitempty"`
	Trusted     []string `json:"trusted,omitempty"` // fingerprints of the paired devices (status)
	Paired      *int     `json:"paired,omitempty"`
	RequireAuth *bool    `json:"requireAuth,omitempty"` // true: strict mode
	TestMode    bool     `json:"testMode,omitempty"`
	Source      string   `json:"source,omitempty"`
	InputMode   string   `json:"inputMode,omitempty"`
	Frames      *uint64  `json:"frames,omitempty"`
	Inputs      *uint64  `json:"inputs,omitempty"`
	Uptime      *int     `json:"uptime,omitempty"`  // seconds
	Dropped     *int     `json:"dropped,omitempty"` // sessions ended by switching to strict mode
}

// Command is one line a controller writes.
type Command struct {
	Schema      int    `json:"schema,omitempty"` // the newest schema the controller knows; optional
	ID          reqID  `json:"id,omitempty"`     // echoed in the answer; a string or a number
	Cmd         string `json:"cmd"`
	Seconds     int    `json:"seconds,omitempty"`     // pair
	Fingerprint string `json:"fingerprint,omitempty"` // untrust
	Value       int    `json:"value,omitempty"`       // fps
	Mode        string `json:"mode,omitempty"`        // mode: "strict" or "open"
}

// reqID is a request id. Controllers write them as strings or as numbers; either
// is echoed back as a string.
type reqID string

const maxIDLen = 64

func (r *reqID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*r = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if len(v) > maxIDLen {
			return errors.New("id is longer than 64 bytes")
		}
		*r = reqID(v)
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return errors.New("id must be a string or a number")
		}
		if len(n) > maxIDLen {
			return errors.New("id is longer than 64 bytes")
		}
		*r = reqID(n)
	}
	return nil
}
