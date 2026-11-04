// Package sitestats counts connections and bytes per user and website in
// memory. Nothing is logged or written to disk; the service sends the totals
// to the panel in batches.
package sitestats

import (
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/publicsuffix"
)

const (
	// SiteIP groups connections whose client sent an IP instead of a domain.
	SiteIP = "[ip]"
	// SiteOther groups hosts that are not valid domains, and new sites once
	// maxEntries is reached.
	SiteOther = "[other]"

	maxSiteLen = 100
	// maxEntries bounds memory between two flushes (roughly 20 MB when full)
	// so a client requesting random domains cannot exhaust the node.
	maxEntries = 200_000
)

type key struct {
	uid  int
	site string
}

// Counter holds the totals of one user and site. A nil Counter ignores all
// calls, so connections without a known user need no special handling.
type Counter struct {
	conns, up, down, active atomic.Int64
}

// AddUp counts bytes sent by the user.
func (c *Counter) AddUp(n int64) {
	if c != nil {
		c.up.Add(n)
	}
}

// AddDown counts bytes received by the user.
func (c *Counter) AddDown(n int64) {
	if c != nil {
		c.down.Add(n)
	}
}

// Close marks one connection as finished.
func (c *Counter) Close() {
	if c != nil {
		c.active.Add(-1)
	}
}

// Stats is safe for concurrent use. A nil Stats records nothing.
type Stats struct {
	mu      sync.Mutex
	entries map[key]*Counter
}

// New creates an empty Stats.
func New() *Stats {
	return &Stats{entries: make(map[key]*Counter)}
}

// Open records a new connection of uid to host. An empty host means the client
// sent an IP. The caller adds bytes to the returned Counter and closes it when
// the connection ends.
func (s *Stats) Open(uid int, host string) *Counter {
	if s == nil || uid <= 0 {
		return nil
	}
	site := Normalize(host)
	s.mu.Lock()
	c := s.counter(uid, site)
	c.conns.Add(1)
	c.active.Add(1)
	s.mu.Unlock()
	return c
}

// counter requires s.mu to be held.
func (s *Stats) counter(uid int, site string) *Counter {
	k := key{uid, site}
	if c, ok := s.entries[k]; ok {
		return c
	}
	if len(s.entries) >= maxEntries {
		k.site = SiteOther
		if c, ok := s.entries[k]; ok {
			return c
		}
	}
	c := &Counter{}
	s.entries[k] = c
	return c
}

// Flush returns the totals since the previous flush as
// site → [][uid, connections, upload, download] and resets them. Sites are not
// repeated per user, which keeps the report small. Open connections stay
// tracked so their later bytes count in the next flush.
func (s *Stats) Flush() map[string][][4]int64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string][][4]int64)
	for k, c := range s.entries {
		conns, up, down := c.conns.Swap(0), c.up.Swap(0), c.down.Swap(0)
		if conns != 0 || up != 0 || down != 0 {
			result[k.site] = append(result[k.site], [4]int64{int64(k.uid), conns, up, down})
		}
		if c.active.Load() <= 0 {
			delete(s.entries, k)
		}
	}
	return result
}

// Restore adds back the result of a Flush whose report failed.
func (s *Stats) Restore(data map[string][][4]int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for site, rows := range data {
		for _, r := range rows {
			c := s.counter(int(r[0]), site)
			c.conns.Add(r[1])
			c.up.Add(r[2])
			c.down.Add(r[3])
		}
	}
}

// Normalize reduces a host to its registrable domain, so www.youtube.com and
// m.youtube.com count as youtube.com.
func Normalize(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return SiteIP
	}
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return SiteIP
	}
	if len(host) > 253 || !strings.Contains(host, ".") || !validHost(host) {
		return SiteOther
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		site = host // the host is itself a public suffix
	}
	if len(site) > maxSiteLen {
		return SiteOther
	}
	return site
}

func validHost(host string) bool {
	for i := 0; i < len(host); i++ {
		c := host[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
