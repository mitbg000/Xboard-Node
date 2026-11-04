package singbox

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/kernel"
	"github.com/cedar2025/xboard-node/internal/model"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/service"
)

const (
	testUserUUID = "5b1f4b1e-6f6f-4a43-9c55-0a3c5f6c2d11"
	testDomain   = "socks.reload.test"
)

// fakeSocks is an upstream SOCKS5 proxy that answers every CONNECT with its name.
func fakeSocks(t *testing.T, name string) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if socksAccept(conn) == nil {
					conn.Write([]byte(name))
				}
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

// socksAccept does the server side of a no-auth SOCKS5 CONNECT to a domain.
func socksAccept(conn net.Conn) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, make([]byte, head[1])); err != nil {
		return err
	}
	conn.Write([]byte{5, 0})
	req := make([]byte, 5)
	if _, err := io.ReadFull(conn, req); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, make([]byte, int(req[4])+2)); err != nil {
		return err
	}
	_, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

// dialThroughNode connects through the node's SOCKS inbound and returns what the
// destination answered, or "" when the node could not reach it.
func dialThroughNode(t *testing.T, port int, domain string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial node: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	conn.Write([]byte{5, 1, 2})
	if _, err := io.ReadFull(conn, make([]byte, 2)); err != nil {
		t.Fatalf("method reply: %v", err)
	}
	auth := []byte{1, byte(len(testUserUUID))}
	auth = append(auth, testUserUUID...)
	auth = append(auth, byte(len(testUserUUID)))
	auth = append(auth, testUserUUID...)
	conn.Write(auth)
	authReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, authReply); err != nil || authReply[1] != 0 {
		t.Fatalf("auth reply %v: %v", authReply, err)
	}

	req := []byte{5, 1, 0, 3, byte(len(domain))}
	req = append(req, domain...)
	req = binary.BigEndian.AppendUint16(req, 80)
	conn.Write(req)
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[1] != 0 {
		return ""
	}
	body, _ := io.ReadAll(conn)
	return string(body)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// nodeSpec routes testDomain through a SOCKS outbound, in the shape the panel sends.
// With no host it has neither the outbound nor the rule.
func nodeSpec(port int, host string, socksPort int) *model.NodeSpec {
	spec := &model.NodeSpec{Protocol: "socks", ListenIP: "127.0.0.1", ServerPort: port}
	if host == "" {
		return spec
	}
	spec.CustomOutbounds = []model.OutboundConfig{{
		Tag:      "socks-1",
		Protocol: "socks",
		Settings: map[string]any{"servers": []any{map[string]any{"address": host, "port": socksPort}}},
	}}
	spec.CustomRouteRules = []model.CustomRouteRule{{
		Match:  model.RouteMatch{Domains: []string{testDomain}},
		Action: model.RouteAction{Type: "route", Target: "socks-1"},
	}}
	return spec
}

func startNode(t *testing.T, spec *model.NodeSpec) (*SingBox, []model.UserSpec) {
	t.Helper()
	users := []model.UserSpec{{ID: 1, UUID: testUserUUID}}
	s := New(config.KernelConfig{LogLevel: "error", ConfigDir: t.TempDir()})
	if err := s.Start(spec, users, kernel.TLSCert{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s, users
}

func TestReloadSwitchesToChangedSocks(t *testing.T) {
	hostA, portA := fakeSocks(t, "A")
	hostB, portB := fakeSocks(t, "B")
	port := freePort(t)

	s, users := startNode(t, nodeSpec(port, hostA, portA))
	if got := dialThroughNode(t, port, testDomain); got != "A" {
		t.Fatalf("before reload: got %q, want A", got)
	}
	if err := s.Reload(nodeSpec(port, hostB, portB), users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := dialThroughNode(t, port, testDomain); got != "B" {
		t.Fatalf("after reload: got %q, want B", got)
	}
}

func TestReloadAddsNewSocks(t *testing.T) {
	host, socksPort := fakeSocks(t, "A")
	port := freePort(t)

	s, users := startNode(t, nodeSpec(port, "", 0))
	if err := s.Reload(nodeSpec(port, host, socksPort), users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := dialThroughNode(t, port, testDomain); got != "A" {
		t.Fatalf("after reload: got %q, want A", got)
	}
}

func TestReloadRemovesSocks(t *testing.T) {
	host, socksPort := fakeSocks(t, "A")
	port := freePort(t)

	s, users := startNode(t, nodeSpec(port, host, socksPort))
	if err := s.Reload(nodeSpec(port, "", 0), users, kernel.TLSCert{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := dialThroughNode(t, port, testDomain); got == "A" {
		t.Fatal("after reload: traffic still goes through the removed SOCKS")
	}
	if _, ok := service.FromContext[adapter.OutboundManager](s.ctx).Outbound("socks-1"); ok {
		t.Fatal("after reload: removed outbound is still registered")
	}
}
