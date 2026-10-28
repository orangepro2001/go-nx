package remote

import (
	"sync"
)

// Gate caps how many connections may be waiting to be authenticated at once, in
// total and per source address. A connection holds a slot from the moment Serve
// starts until the host has decided about it (or it gives up), so someone who
// opens connections and says nothing cannot crowd out real viewers or make the
// host spend its time on them.
type Gate struct {
	mu        sync.Mutex
	total     int
	perSource int
	n         int
	by        map[string]int
}

// NewGate allows total connections in all and perSource from one address.
func NewGate(total, perSource int) *Gate {
	return &Gate{total: total, perSource: perSource, by: map[string]int{}}
}

// Enter takes a slot for the connection from addr. If one is free it returns a
// function that gives the slot back (calling it again does nothing) and true.
func (g *Gate) Enter(addr string) (leave func(), ok bool) {
	key := sourceHost(addr)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n >= g.total || g.by[key] >= g.perSource {
		return nil, false
	}
	g.n++
	g.by[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.n--
			if g.by[key]--; g.by[key] <= 0 {
				delete(g.by, key)
			}
		})
	}, true
}
