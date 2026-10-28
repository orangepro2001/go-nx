package hostd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// MaxLineBytes is the longest command line accepted. A longer one is skipped, with
// an error event, and the next line is read as usual.
const MaxLineBytes = 64 << 10

const (
	defaultPairing = 5 * time.Minute
	maxPairing     = time.Hour
)

// ReadCommands reads commands, one JSON object per line, from r until it ends, and
// has h carry them out. The end of r means the controller is gone, and stops the
// host.
func ReadCommands(r io.Reader, h *Host) {
	br := bufio.NewReaderSize(r, 4096)
	for {
		line, tooLong, err := readLine(br, MaxLineBytes)
		switch {
		case tooLong:
			h.reply("", Event{Event: "error", ErrorCode: "line-too-long", Message: fmt.Sprintf("a command line is at most %d bytes", MaxLineBytes)})
		case len(strings.TrimSpace(string(line))) > 0:
			h.Handle(line)
		}
		if err != nil {
			break
		}
	}
	h.Stop("controller-closed")
}

// readLine reads one line, keeping at most max bytes of it. A longer line is read
// to its end and discarded, and reported as too long.
func readLine(br *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		part, isPrefix, rerr := br.ReadLine()
		if rerr != nil {
			return line, tooLong, rerr
		}
		if !tooLong {
			if len(line)+len(part) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, part...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}

// reply emits ev with the id of the command it answers.
func (h *Host) reply(id reqID, ev Event) {
	ev.ID = string(id)
	h.em.Emit(ev)
}

func (h *Host) fail(id reqID, code, format string, args ...any) {
	h.reply(id, Event{Event: "error", ErrorCode: code, Message: fmt.Sprintf(format, args...)})
}

// Handle carries out one command line. Whatever is wrong with it is answered with
// an error event; it never stops the host.
func (h *Host) Handle(line []byte) {
	var c Command
	if err := json.Unmarshal(line, &c); err != nil {
		// The id may still be readable even if something else is not.
		var only struct{ ID reqID }
		_ = json.Unmarshal(line, &only)
		h.fail(only.ID, "bad-json", "not a command: %v", err)
		return
	}
	if c.Schema > SchemaVersion {
		h.fail(c.ID, "unsupported-schema", "this host speaks control schema %d, the controller asked for %d", SchemaVersion, c.Schema)
		return
	}
	switch c.Cmd {
	case "":
		h.fail(c.ID, "bad-command", `a command has a "cmd"`)
	case "status":
		h.reply(c.ID, h.status())
	case "pair":
		window := defaultPairing
		if c.Seconds != 0 {
			if c.Seconds < 1 || time.Duration(c.Seconds)*time.Second > maxPairing {
				h.fail(c.ID, "bad-argument", "seconds must be from 1 to %d", int(maxPairing.Seconds()))
				return
			}
			window = time.Duration(c.Seconds) * time.Second
		}
		code := newCode()
		h.policy.OpenPairing(code, window)
		h.log.Printf("pairing window open for %v", window)
		h.reply(c.ID, Event{Event: "pairing", Code: code, ExpiresIn: ptr(int(window.Seconds()))})
	case "pair-cancel":
		h.policy.ClosePairing()
		h.reply(c.ID, Event{Event: "pairing", ExpiresIn: ptr(0)})
	case "untrust":
		fp := strings.ToLower(strings.TrimSpace(c.Fingerprint))
		if fp == "" {
			h.fail(c.ID, "bad-argument", "untrust needs a fingerprint")
			return
		}
		if _, ok := h.store.Get(fp); !ok {
			h.fail(c.ID, "not-found", "no paired device has that fingerprint")
			return
		}
		if err := h.policy.Revoke(fp); err != nil {
			h.fail(c.ID, "internal", "could not save the paired devices: %v", err)
			return
		}
		h.kick(fp, remote.CloseRevoked, "removed from the paired devices")
		h.reply(c.ID, h.status())
	case "untrust-all":
		n, err := h.policy.RevokeAll()
		h.kick("", remote.CloseRevoked, "removed from the paired devices")
		if err != nil {
			h.fail(c.ID, "internal", "removed %d device(s) but could not save: %v", n, err)
			return
		}
		h.reply(c.ID, h.status())
	case "fps":
		if c.Value < 1 || c.Value > MaxControlFPS {
			h.fail(c.ID, "bad-argument", "value must be from 1 to %d", MaxControlFPS)
			return
		}
		h.fps.Store(int32(c.Value))
		h.reply(c.ID, h.status())
	case "mode":
		var strict bool
		switch c.Mode {
		case "strict":
			strict = true
		case "open":
		default:
			h.fail(c.ID, "bad-argument", `mode must be "strict" or "open"`)
			return
		}
		h.policy.SetRequireAuth(strict)
		dropped := 0
		if strict {
			dropped = h.sessions.DropUnverified(remote.CloseUnverified, "the host now requires verification")
		}
		h.log.Printf("mode: %s (ended %d unverified session(s))", c.Mode, dropped)
		st := h.status()
		st.Dropped = ptr(dropped)
		h.reply(c.ID, st)
	case "stop":
		h.reply(c.ID, Event{Event: "ack"})
		h.Stop("stop")
	default:
		h.fail(c.ID, "unknown-command", "no command called %q", c.Cmd)
	}
}

func newCode() string { return trust.NewPairingCode() }
