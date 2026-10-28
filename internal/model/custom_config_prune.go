package model

import (
	"strings"

	"github.com/cedar2025/xboard-node/internal/nlog"
)

// pruneInvalidCustomConfig drops panel custom outbounds and route rules that
// would fail validation (e.g. a SOCKS outbound without settings, or a rule
// routing to an outbound that does not exist) instead of rejecting the whole
// node config. The rest of the node keeps running; affected traffic falls back
// to the default outbound. Each dropped entry is logged as a warning.
func pruneInvalidCustomConfig(n *NodeSpec, kernelType string, additional []outboundTagSource, additionalTags []string) {
	kept := make([]OutboundConfig, 0, len(n.CustomOutbounds))
	seen := map[string]struct{}{}
	for i, outbound := range n.CustomOutbounds {
		alone := outbound
		alone.ProxyTag = "" // checked below, once every outbound is known
		err := ValidateCustomOutboundsForKernel([]OutboundConfig{alone}, kernelType, additionalTags)
		if err == nil {
			err = validateOutboundTagCollisions([]OutboundConfig{alone}, additional)
		}
		key := strings.ToLower(strings.TrimSpace(outbound.Tag))
		if _, dup := seen[key]; err == nil && dup {
			err = errDuplicateTag
		}
		if err != nil {
			nlog.Core().Warn("custom outbound ignored", "index", i, "tag", outbound.Tag, "error", err)
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, outbound)
	}

	// Drop outbounds chained to a missing outbound, repeating until stable.
	for changed := true; changed; {
		changed = false
		available := buildAvailableOutboundTags(kept, additionalTags)
		next := kept[:0]
		for _, outbound := range kept {
			proxyTag := strings.ToLower(strings.TrimSpace(outbound.ProxyTag))
			_, ok := available[proxyTag]
			if outbound.ProxyTag != "" && (!ok || proxyTag == strings.ToLower(strings.TrimSpace(outbound.Tag))) {
				nlog.Core().Warn("custom outbound ignored", "tag", outbound.Tag, "error", "proxy_tag references unknown outbound "+outbound.ProxyTag)
				changed = true
				continue
			}
			next = append(next, outbound)
		}
		kept = next
	}
	n.CustomOutbounds = kept

	available := buildAvailableOutboundTags(kept, additionalTags)
	rules := make([]CustomRouteRule, 0, len(n.CustomRouteRules))
	for i, rule := range n.CustomRouteRules {
		if err := ValidateCustomRouteRules([]CustomRouteRule{rule}, kernelType, available); err != nil {
			nlog.Core().Warn("custom route rule ignored", "index", i, "name", rule.Name, "error", err)
			continue
		}
		rules = append(rules, rule)
	}
	n.CustomRouteRules = rules
}

type pruneError string

func (e pruneError) Error() string { return string(e) }

const errDuplicateTag = pruneError("tag duplicates an earlier custom outbound")
