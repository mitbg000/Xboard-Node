package sitestats

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"www.YouTube.com.":                "youtube.com",
		"a.b.example.co.uk":               "example.co.uk",
		"foo.github.io":                   "foo.github.io",
		"":                                SiteIP,
		"1.1.1.1":                         SiteIP,
		"[2001:db8::1]":                   SiteIP,
		"localhost":                       SiteOther,
		"bad host.com":                    SiteOther,
		"<script>.com":                    SiteOther,
		"co.uk":                           "co.uk",
		strings.Repeat("a", 120) + ".com": SiteOther,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlushMergesHostsAndResets(t *testing.T) {
	s := New()
	a := s.Open(1, "www.youtube.com")
	b := s.Open(1, "m.youtube.com")
	s.Open(2, "youtube.com").Close()
	a.AddUp(10)
	b.AddDown(20)
	a.Close()
	b.Close()

	rows := s.Flush()["youtube.com"]
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	want := [][4]int64{{1, 2, 10, 20}, {2, 1, 0, 0}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	if got := s.Flush(); len(got) != 0 {
		t.Fatalf("second flush = %v, want empty", got)
	}
	if len(s.entries) != 0 {
		t.Fatalf("closed entries kept: %d", len(s.entries))
	}
}

func TestOpenConnectionKeepsCountingAfterFlush(t *testing.T) {
	s := New()
	c := s.Open(1, "example.com")
	c.AddDown(5)
	s.Flush()
	c.AddDown(7)
	c.Close()

	want := map[string][][4]int64{"example.com": {{1, 0, 0, 7}}}
	if got := s.Flush(); !reflect.DeepEqual(got, want) {
		t.Fatalf("flush = %v, want %v", got, want)
	}
}

func TestRestoreAddsBack(t *testing.T) {
	s := New()
	s.Open(1, "example.com").Close()
	data := s.Flush()
	s.Open(1, "example.com").Close()
	s.Restore(data)

	want := map[string][][4]int64{"example.com": {{1, 2, 0, 0}}}
	if got := s.Flush(); !reflect.DeepEqual(got, want) {
		t.Fatalf("flush = %v, want %v", got, want)
	}
}

func TestEntryLimitGroupsNewSites(t *testing.T) {
	s := New()
	for i := 0; i < maxEntries; i++ {
		s.entries[key{i + 1, "x"}] = &Counter{}
	}
	s.Open(1, "new-site.com").Close()
	s.Open(1, "another-site.com").Close()

	if rows := s.Flush()[SiteOther]; len(rows) != 1 || rows[0][1] != 2 {
		t.Fatalf("other rows = %v", rows)
	}
}

func TestNilIsSafe(t *testing.T) {
	var s *Stats
	c := s.Open(1, "example.com")
	c.AddUp(1)
	c.AddDown(1)
	c.Close()
	s.Restore(s.Flush())
	if New().Open(0, "example.com") != nil {
		t.Fatal("unknown user must not be counted")
	}
}
