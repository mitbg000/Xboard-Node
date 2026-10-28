package model

import "testing"

func TestClassifyRouteMatch(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		isCIDR bool
	}{
		{"10.0.0.0/8", "10.0.0.0/8", true},
		{"2001:db8::/32", "2001:db8::/32", true},
		{"example.com", "example.com", false},
		{"rfa.org/chinese", "rfa.org", false},
		{"https://example.com/path?q=1", "example.com", false},
	}
	for _, c := range cases {
		got, isCIDR := ClassifyRouteMatch(c.in)
		if got != c.want || isCIDR != c.isCIDR {
			t.Errorf("ClassifyRouteMatch(%q) = %q, %v; want %q, %v", c.in, got, isCIDR, c.want, c.isCIDR)
		}
	}
}
