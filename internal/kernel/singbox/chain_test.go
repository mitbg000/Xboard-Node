package singbox

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cedar2025/xboard-node/internal/kernel"
	"github.com/cedar2025/xboard-node/internal/model"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/service"
)

// socksHop is an upstream SOCKS5 proxy that relays each CONNECT to its real
// destination and counts them, so a test can see traffic pass through it.
type socksHop struct {
	host    string
	port    int
	connect atomic.Int32
}

func newSocksHop(t *testing.T) *socksHop {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	addr := ln.Addr().(*net.TCPAddr)
	hop := &socksHop{host: addr.IP.String(), port: addr.Port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go hop.relay(conn)
		}
	}()
	return hop
}

func (h *socksHop) relay(conn net.Conn) {
	defer conn.Close()
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, head[1])); err != nil {
		return
	}
	conn.Write([]byte{5, 0})

	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[3] != 1 {
		return // the outbound chained through a hop dials it by IPv4
	}
	target := make([]byte, 6)
	if _, err := io.ReadFull(conn, target); err != nil {
		return
	}
	dest := net.JoinHostPort(net.IP(target[:4]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(target[4:]))))
	upstream, err := net.Dial("tcp", dest)
	if err != nil {
		return
	}
	defer upstream.Close()
	h.connect.Add(1)
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

	go io.Copy(upstream, conn)
	io.Copy(conn, upstream)
}

func socksOutbound(tag, host string, port int, proxyTag string) model.OutboundConfig {
	return model.OutboundConfig{
		Tag:      tag,
		Protocol: "socks",
		ProxyTag: proxyTag,
		Settings: map[string]any{"servers": []any{map[string]any{"address": host, "port": port}}},
	}
}

// chainSpec routes testDomain to the first outbound.
func chainSpec(port int, outbounds ...model.OutboundConfig) *model.NodeSpec {
	return &model.NodeSpec{
		Protocol: "socks", ListenIP: "127.0.0.1", ServerPort: port,
		CustomOutbounds: outbounds,
		CustomRouteRules: []model.CustomRouteRule{{
			Match:  model.RouteMatch{Domains: []string{testDomain}},
			Action: model.RouteAction{Type: "route", Target: outbounds[0].Tag},
		}},
	}
}

func outboundManager(s *SingBox) adapter.OutboundManager {
	return service.FromContext[adapter.OutboundManager](s.ctx)
}

func TestStartChainsThroughProxyTag(t *testing.T) {
	exitHost, exitPort := fakeSocks(t, "exit")
	hop := newSocksHop(t)
	port := freePort(t)

	startNode(t, chainSpec(port,
		socksOutbound("exit", exitHost, exitPort, "hop"),
		socksOutbound("hop", hop.host, hop.port, ""),
	))

	if got := dialThroughNode(t, port, testDomain); got != "exit" {
		t.Fatalf("got %q, want exit", got)
	}
	if hop.connect.Load() == 0 {
		t.Fatal("traffic did not go through the chained proxy")
	}
}

func TestStartProxyTagToBuiltinDirectDialsDirectly(t *testing.T) {
	exitHost, exitPort := fakeSocks(t, "exit")
	port := freePort(t)

	startNode(t, chainSpec(port, socksOutbound("exit", exitHost, exitPort, "direct")))

	if got := dialThroughNode(t, port, testDomain); got != "exit" {
		t.Fatalf("got %q, want exit", got)
	}
}

// The chained outbound's own config does not change here, yet its dialer holds the
// old target until it is rebuilt.
func TestReloadRebuildsDependentsWhenTargetChanges(t *testing.T) {
	exitHost, exitPort := fakeSocks(t, "exit")
	hopA, hopB := newSocksHop(t), newSocksHop(t)
	port := freePort(t)
	exit := socksOutbound("exit", exitHost, exitPort, "hop")

	s, users := startNode(t, chainSpec(port, exit, socksOutbound("hop", hopA.host, hopA.port, "")))
	if got := dialThroughNode(t, port, testDomain); got != "exit" {
		t.Fatalf("before reload: got %q, want exit", got)
	}
	before := hopA.connect.Load()

	spec := chainSpec(port, exit, socksOutbound("hop", hopB.host, hopB.port, ""))
	if err := s.Reload(spec, users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := dialThroughNode(t, port, testDomain); got != "exit" {
		t.Fatalf("after reload: got %q, want exit", got)
	}
	if hopB.connect.Load() == 0 {
		t.Fatal("after reload: traffic did not go through the new target")
	}
	if hopA.connect.Load() != before {
		t.Fatal("after reload: traffic still goes through the old target")
	}
}

func TestReloadRebuildsWholeChainWhenLastTargetChanges(t *testing.T) {
	exitHost, exitPort := fakeSocks(t, "exit")
	middle, oldLast, newLast := newSocksHop(t), newSocksHop(t), newSocksHop(t)
	port := freePort(t)
	exit := socksOutbound("exit", exitHost, exitPort, "middle")
	mid := socksOutbound("middle", middle.host, middle.port, "last")

	s, users := startNode(t, chainSpec(port, exit, mid, socksOutbound("last", oldLast.host, oldLast.port, "")))
	spec := chainSpec(port, exit, mid, socksOutbound("last", newLast.host, newLast.port, ""))
	if err := s.Reload(spec, users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := dialThroughNode(t, port, testDomain); got != "exit" {
		t.Fatalf("got %q, want exit", got)
	}
	if newLast.connect.Load() == 0 || oldLast.connect.Load() != 0 {
		t.Fatalf("connects: new %d, old %d", newLast.connect.Load(), oldLast.connect.Load())
	}
}

// Without removing the dependent first, sing-box keeps listing it under the old
// target and refuses to close that target later.
func TestReloadForgetsOldDependency(t *testing.T) {
	exitHost, exitPort := fakeSocks(t, "exit")
	hopA, hopB := newSocksHop(t), newSocksHop(t)
	port := freePort(t)
	targetA := socksOutbound("hop-a", hopA.host, hopA.port, "")
	targetB := socksOutbound("hop-b", hopB.host, hopB.port, "")

	s, users := startNode(t, chainSpec(port, socksOutbound("exit", exitHost, exitPort, "hop-a"), targetA, targetB))
	spec := chainSpec(port, socksOutbound("exit", exitHost, exitPort, "hop-b"), targetA, targetB)
	if err := s.Reload(spec, users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := dialThroughNode(t, port, testDomain); got != "exit" {
		t.Fatalf("got %q, want exit", got)
	}
	if hopB.connect.Load() == 0 || hopA.connect.Load() != 0 {
		t.Fatalf("connects: b %d, a %d", hopB.connect.Load(), hopA.connect.Load())
	}
	if err := outboundManager(s).Remove("hop-a"); err != nil {
		t.Fatalf("old target still listed as depended on: %v", err)
	}
}

func TestReloadRemovesChainDependentsFirst(t *testing.T) {
	exitHost, exitPort := fakeSocks(t, "exit")
	hop := newSocksHop(t)
	port := freePort(t)

	s, users := startNode(t, chainSpec(port,
		socksOutbound("exit", exitHost, exitPort, "hop"),
		socksOutbound("hop", hop.host, hop.port, ""),
	))
	keep := socksOutbound("keep", exitHost, exitPort, "")
	if err := s.Reload(chainSpec(port, keep), users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, tag := range []string{"exit", "hop"} {
		if _, ok := outboundManager(s).Outbound(tag); ok {
			t.Fatalf("outbound %s is still registered", tag)
		}
	}
	// A new outbound reusing the removed tags must start from a clean slate.
	if err := s.Reload(chainSpec(port, socksOutbound("hop", hop.host, hop.port, "")), users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := outboundManager(s).Remove("hop"); err != nil {
		t.Fatalf("removed chain left dependencies behind: %v", err)
	}
}

func TestReloadRejectsChainCycleWithoutTouchingOutbounds(t *testing.T) {
	exitHost, exitPort := fakeSocks(t, "exit")
	hop := newSocksHop(t)
	port := freePort(t)

	s, users := startNode(t, chainSpec(port,
		socksOutbound("exit", exitHost, exitPort, "hop"),
		socksOutbound("hop", hop.host, hop.port, ""),
	))
	spec := chainSpec(port,
		socksOutbound("exit", exitHost, exitPort, "hop"),
		socksOutbound("hop", hop.host, hop.port, "exit"),
	)
	err := s.Reload(spec, users, kernel.TLSCert{})
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("got %v, want a circular dependency error", err)
	}
	if got := dialThroughNode(t, port, testDomain); got != "exit" {
		t.Fatalf("after rejected reload: got %q, want exit", got)
	}
}
