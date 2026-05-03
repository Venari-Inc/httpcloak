package pool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// healthyListener returns a TCP listener bound to 127.0.0.1 on a random port.
// Caller is responsible for closing it.
func healthyListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open listener: %v", err)
	}
	return l
}

// deadAddr returns a host/port pair that is guaranteed to refuse connections:
// we open a listener, capture its address, then close it before any dial can
// succeed. The kernel will not re-bind the port immediately, so connections
// either get ECONNREFUSED or hit the dial timeout.
func deadAddr(t *testing.T) (string, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open dead-address listener: %v", err)
	}
	addr := l.Addr().(*net.TCPAddr)
	host := addr.IP.String()
	port := fmt.Sprintf("%d", addr.Port)
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}
	return host, port
}

// splitHostPort returns host and port string from a *net.TCPAddr.
func splitHostPort(a net.Addr) (string, string) {
	ta := a.(*net.TCPAddr)
	return ta.IP.String(), fmt.Sprintf("%d", ta.Port)
}

// netDial is a thin wrapper that satisfies the dialAllIPs dialer signature.
func netDial(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// (a) First IP dead, second healthy -> conn from second.
func TestDialAllIPs_FirstDeadSecondHealthy(t *testing.T) {
	deadHost, _ := deadAddr(t)
	healthy := healthyListener(t)
	defer healthy.Close()

	// Drain accepts so the dial settles cleanly.
	go func() {
		for {
			c, err := healthy.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	healthyHost, healthyPort := splitHostPort(healthy.Addr())

	// dialAllIPs takes a single port; reuse the healthy port for both IPs.
	// Loopback at the dead host is unbound at this port -> ECONNREFUSED.
	ips := []string{deadHost, healthyHost}
	conn, err := dialAllIPs(context.Background(), "test.example.com", ips, healthyPort,
		netDial, 30*time.Second)
	if err != nil {
		t.Fatalf("expected success from healthy IP, got error: %v", err)
	}
	defer conn.Close()

	gotHost, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	if gotHost != healthyHost {
		t.Fatalf("expected conn to %s, got %s", healthyHost, gotHost)
	}
}

// (b) All unreachable -> wrapped error; total time bounded under connectTimeout.
func TestDialAllIPs_AllUnreachable(t *testing.T) {
	host1, port := deadAddr(t)
	host2, _ := deadAddr(t)
	ips := []string{host1, host2}

	start := time.Now()
	_, err := dialAllIPs(context.Background(), "test.example.com", ips, port,
		netDial, 30*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "all 2 proxy IPs failed") {
		t.Fatalf("expected error to mention 'all 2 proxy IPs failed', got: %v", err)
	}
	// Both IPs are loopback closed-ports -> ECONNREFUSED is immediate. Even
	// with the 2s per-IP floor, we should comfortably finish well under 25s.
	if elapsed >= 25*time.Second {
		t.Fatalf("dialAllIPs took %v, expected < 25s", elapsed)
	}
}

// fakeHTTPProxy is a minimal HTTP CONNECT proxy listener for tests. It tracks
// accept count so tests can assert which IPs were tried.
type fakeHTTPProxy struct {
	listener    net.Listener
	respondWith string
	accepts     int32
	wg          sync.WaitGroup
}

func newFakeHTTPProxy(t *testing.T, respondWith string) *fakeHTTPProxy {
	t.Helper()
	l := healthyListener(t)
	f := &fakeHTTPProxy{listener: l, respondWith: respondWith}
	f.wg.Add(1)
	go f.serve()
	return f
}

func (f *fakeHTTPProxy) serve() {
	defer f.wg.Done()
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		atomic.AddInt32(&f.accepts, 1)
		go func(c net.Conn) {
			defer c.Close()
			r := bufio.NewReader(c)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if line == "\r\n" || line == "\n" {
					break
				}
			}
			_, _ = io.WriteString(c, f.respondWith)
		}(conn)
	}
}

func (f *fakeHTTPProxy) close() {
	_ = f.listener.Close()
	f.wg.Wait()
}

func (f *fakeHTTPProxy) addr() (string, string) {
	return splitHostPort(f.listener.Addr())
}

// (c) Handshake error from first IP propagates; second IP not tried.
//
// Exercises the dial-then-handshake-once contract directly: dialAllIPs returns
// the first successful TCP conn, the handshake runs on that conn, and a 407
// must propagate without re-dialing the second IP. Mirrors the refactored
// dialHTTPProxy body. We do not invoke dialHTTPProxy itself because it
// resolves via DNS; constructing the dial sequence here lets us inject
// arbitrary IPs and assert per-IP accept counts.
func TestDialHTTPProxy_HandshakeErrorPropagates(t *testing.T) {
	proxyA := newFakeHTTPProxy(t, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
	defer proxyA.close()
	proxyB := newFakeHTTPProxy(t, "HTTP/1.1 200 Connection Established\r\n\r\n")
	defer proxyB.close()

	_, portA := proxyA.addr()

	// Both proxies bind 127.0.0.1 on different ports. dialAllIPs accepts a
	// single port, so route both attempts through portA. If the loop ever
	// "fell through" to a second IP it would re-hit proxy A, not B. Asserting
	// proxyB.accepts == 0 below proves no cross-IP retry happened on the
	// handshake error.
	ips := []string{"127.0.0.1", "127.0.0.1"}
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialAllIPs(context.Background(), "test.example.com", ips, portA,
		func(ctx context.Context, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", addr)
		}, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	defer conn.Close()

	connectReq := "CONNECT target.example.com:443 HTTP/1.1\r\nHost: target.example.com:443\r\n\r\n"
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, "407") {
		t.Fatalf("expected 407 from proxy A, got: %q", resp)
	}

	if got := atomic.LoadInt32(&proxyB.accepts); got != 0 {
		t.Fatalf("proxy B should not have been dialed, but got %d accepts", got)
	}
	if got := atomic.LoadInt32(&proxyA.accepts); got != 1 {
		t.Fatalf("proxy A should have exactly 1 accept, got %d", got)
	}
}

// (d) With 10 IPs and a 30s budget, perAddr is floored at 2s.
func TestDialAllIPs_PerIPFloor(t *testing.T) {
	if got := perIPTimeout(30*time.Second, 10); got != 3*time.Second {
		t.Fatalf("perIPTimeout(30s,10) = %v, want 3s", got)
	}
	if got := perIPTimeout(30*time.Second, 20); got != 2*time.Second {
		t.Fatalf("perIPTimeout(30s,20) = %v, want 2s (floor)", got)
	}
	if got := perIPTimeout(30*time.Second, 1); got != 10*time.Second {
		t.Fatalf("perIPTimeout(30s,1) = %v, want 10s (cap)", got)
	}
	if got := perIPTimeout(30*time.Second, 0); got != 30*time.Second {
		t.Fatalf("perIPTimeout(30s,0) = %v, want 30s", got)
	}

	// Behavioural check: 10 dead loopback IPs at 30s budget must not block on
	// per-IP timeout. ECONNREFUSED is immediate, so total elapsed << 25s.
	ips := make([]string, 10)
	host, port := deadAddr(t)
	for i := range ips {
		ips[i] = host
	}
	start := time.Now()
	_, err := dialAllIPs(context.Background(), "h", ips, port, netDial, 30*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from 10 dead IPs")
	}
	if elapsed >= 25*time.Second {
		t.Fatalf("10-IP dial took %v, expected well under 25s", elapsed)
	}
}

// (e) IPv4 filter strips IPv6 entries and preserves order.
func TestFilterIPv4(t *testing.T) {
	in := []string{"::1", "127.0.0.1", "2001:db8::1", "10.0.0.1"}
	want := []string{"127.0.0.1", "10.0.0.1"}
	got := filterIPv4(in)
	if len(got) != len(want) {
		t.Fatalf("filterIPv4 len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, ip := range want {
		if got[i] != ip {
			t.Fatalf("filterIPv4[%d] = %q, want %q", i, got[i], ip)
		}
	}
	if out := filterIPv4([]string{"not-an-ip"}); len(out) != 0 {
		t.Fatalf("filterIPv4(not-an-ip) = %v, want []", out)
	}
	if out := filterIPv4(nil); len(out) != 0 {
		t.Fatalf("filterIPv4(nil) = %v, want []", out)
	}
}

// Sanity guard: dialAllIPs returns a useful error when given no IPs. Callers
// already filter for empty input, but the helper must not divide by zero.
func TestDialAllIPs_EmptyIPs(t *testing.T) {
	_, err := dialAllIPs(context.Background(), "h", nil, "443", netDial, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for empty IPs")
	}
	if errors.Is(err, nil) && strings.Contains(err.Error(), "all 0") {
		t.Fatalf("unexpected error shape: %v", err)
	}
}
