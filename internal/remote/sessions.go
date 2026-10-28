package remote

import (
	"sync"

	"github.com/quic-go/quic-go"
)

// Sessions tracks which live sessions of a host were verified and which were let
// in only because the host was in open mode, so that going back to strict mode
// can end exactly the second kind.
type Sessions struct {
	mu     sync.Mutex
	strict func() bool
	live   map[*quic.Conn]bool // true: admitted without verification
}

// NewSessions makes an empty tracker. strict reports whether the host currently
// requires verification; it decides whether an unverified session may still join.
func NewSessions(strict func() bool) *Sessions {
	return &Sessions{strict: strict, live: map[*quic.Conn]bool{}}
}

// add registers a session. An unverified one is refused (ok=false) once the host
// is strict. That check happens under the same lock DropUnverified takes, so a
// session that passed the policy a moment before the switch is either refused
// here or registered in time to be dropped: it cannot slip between the two.
func (s *Sessions) add(conn *quic.Conn, unverified bool) (remove func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if unverified && s.strict() {
		return nil, false
	}
	s.live[conn] = unverified
	return func() {
		s.mu.Lock()
		delete(s.live, conn)
		s.mu.Unlock()
	}, true
}

// DropUnverified closes every session that was admitted without verification and
// returns how many it ended. Call it after the policy has switched to strict.
func (s *Sessions) DropUnverified(code quic.ApplicationErrorCode, reason string) int {
	s.mu.Lock()
	var drop []*quic.Conn
	for conn, unverified := range s.live {
		if unverified {
			drop = append(drop, conn)
		}
	}
	s.mu.Unlock()
	for _, conn := range drop {
		conn.CloseWithError(code, reason)
	}
	return len(drop)
}

// Count returns how many live sessions are verified and how many are not.
func (s *Sessions) Count() (verified, unverified int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.live {
		if u {
			unverified++
		} else {
			verified++
		}
	}
	return
}
