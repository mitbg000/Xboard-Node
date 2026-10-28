package model

import (
	"testing"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/panel"
)

func TestNodeSpecFromPanelValidated(t *testing.T) {
	t.Run("valid custom outbounds and route targets", func(t *testing.T) {
		node, err := NodeSpecFromPanelValidated(&panel.NodeConfig{
			Protocol:   "shadowsocks",
			ServerPort: 8388,
			CustomOutbounds: []panel.OutboundConfig{
				{Tag: "warp", Protocol: "wireguard", Settings: map[string]any{"server": "1.1.1.1", "server_port": 2408, "private_key": "pk"}},
				{Tag: "proxy", Protocol: "socks", ProxyTag: "warp", Settings: map[string]any{"server": "2.2.2.2", "server_port": 1080}},
			},
			CustomRouteRules: []panel.CustomRouteRule{{
				Action: panel.RouteAction{Type: "route", Target: "proxy"},
				Match:  panel.RouteMatch{DomainSuffixes: []string{"example.com"}},
			}},
		}, config.KernelConfig{Type: "singbox"})
		if err != nil {
			t.Fatalf("NodeSpecFromPanelValidated: %v", err)
		}
		if node == nil || len(node.CustomOutbounds) != 2 {
			t.Fatalf("unexpected node spec: %#v", node)
		}
	})

	t.Run("drops outbound chained to a missing outbound", func(t *testing.T) {
		node, err := NodeSpecFromPanelValidated(&panel.NodeConfig{
			Protocol:   "shadowsocks",
			ServerPort: 8388,
			CustomOutbounds: []panel.OutboundConfig{{
				Tag: "proxy", Protocol: "socks", ProxyTag: "missing", Settings: map[string]any{"server": "2.2.2.2", "server_port": 1080},
			}},
		}, config.KernelConfig{Type: "singbox"})
		if err != nil {
			t.Fatalf("expected the invalid outbound to be dropped, got %v", err)
		}
		if len(node.CustomOutbounds) != 0 {
			t.Fatalf("expected no outbounds, got %#v", node.CustomOutbounds)
		}
	})

	t.Run("drops outbound without settings and rules routing to it", func(t *testing.T) {
		node, err := NodeSpecFromPanelValidated(&panel.NodeConfig{
			Protocol:   "shadowsocks",
			ServerPort: 8388,
			CustomOutbounds: []panel.OutboundConfig{
				{Tag: "socks", Protocol: "socks"},
				{Tag: "warp", Protocol: "wireguard", Settings: map[string]any{"server": "1.1.1.1", "server_port": 2408, "private_key": "pk"}},
			},
			CustomRouteRules: []panel.CustomRouteRule{
				{Action: panel.RouteAction{Type: "route", Target: "socks"}, Match: panel.RouteMatch{DomainSuffixes: []string{"instagram.com"}}},
				{Action: panel.RouteAction{Type: "route", Target: "warp"}, Match: panel.RouteMatch{DomainSuffixes: []string{"example.com"}}},
			},
		}, config.KernelConfig{Type: "singbox"})
		if err != nil {
			t.Fatalf("expected the invalid outbound to be dropped, got %v", err)
		}
		if len(node.CustomOutbounds) != 1 || node.CustomOutbounds[0].Tag != "warp" {
			t.Fatalf("expected only warp to remain, got %#v", node.CustomOutbounds)
		}
		if len(node.CustomRouteRules) != 1 || node.CustomRouteRules[0].Action.Target != "warp" {
			t.Fatalf("expected only the warp rule to remain, got %#v", node.CustomRouteRules)
		}
	})

	t.Run("drops protocol unsupported by kernel", func(t *testing.T) {
		node, err := NodeSpecFromPanelValidated(&panel.NodeConfig{
			Protocol:   "shadowsocks",
			ServerPort: 8388,
			CustomOutbounds: []panel.OutboundConfig{{
				Tag: "hy2", Protocol: "hysteria2", Settings: map[string]any{"server": "2.2.2.2", "server_port": 8443},
			}},
		}, config.KernelConfig{Type: "xray"})
		if err != nil {
			t.Fatalf("expected the unsupported outbound to be dropped, got %v", err)
		}
		if len(node.CustomOutbounds) != 0 {
			t.Fatalf("expected no outbounds, got %#v", node.CustomOutbounds)
		}
	})

	t.Run("drops rule with unknown route target", func(t *testing.T) {
		node, err := NodeSpecFromPanelValidated(&panel.NodeConfig{
			Protocol:   "shadowsocks",
			ServerPort: 8388,
			CustomOutbounds: []panel.OutboundConfig{{
				Tag: "warp", Protocol: "wireguard", Settings: map[string]any{"server": "1.1.1.1", "server_port": 2408, "private_key": "pk"},
			}},
			CustomRouteRules: []panel.CustomRouteRule{{
				Action: panel.RouteAction{Type: "route", Target: "missing"},
				Match:  panel.RouteMatch{DomainSuffixes: []string{"example.com"}},
			}},
		}, config.KernelConfig{Type: "singbox"})
		if err != nil {
			t.Fatalf("expected the rule to be dropped, got %v", err)
		}
		if len(node.CustomOutbounds) != 1 || len(node.CustomRouteRules) != 0 {
			t.Fatalf("expected the outbound kept and the rule dropped, got %#v", node)
		}
	})
}
