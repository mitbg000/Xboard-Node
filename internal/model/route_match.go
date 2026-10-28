package model

import (
	"net/netip"
	"strings"
)

// ClassifyRouteMatch tells whether a panel route match entry is an IP CIDR or a
// domain. Only entries that parse as a prefix count as CIDRs; URL-like entries
// such as "rfa.org/chinese" or "https://example.com/path" are reduced to their
// host, since the node can only match traffic by domain, not by URL path.
func ClassifyRouteMatch(item string) (value string, isCIDR bool) {
	if _, err := netip.ParsePrefix(item); err == nil {
		return item, true
	}
	if i := strings.Index(item, "://"); i >= 0 {
		item = item[i+3:]
	}
	if i := strings.IndexAny(item, "/?#"); i >= 0 {
		item = item[:i]
	}
	return item, false
}
