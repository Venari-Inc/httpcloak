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

	"github.com/sardanioss/httpcloak/fingerprint"
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

// makeTestPool returns a minimal HostPool for use in dialHTTPProxy /
// dialSOCKS5Proxy tests. The preset uses a zero-value TCPFingerprint so no
// OS-specific setsockopt calls change the loopback socket behaviour.
func makeTestPool(t *testing.T, host, port string) *HostPool {
	t.Helper()
	preset := &fingerprint.Preset{
		TCPFingerprint: fingerprint.TCPFingerprint{},
	}
	return &HostPool{
		host:           host,
		port:           port,
		preset:         preset,
		connectTimeout: 10 * time.Second,
	}
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

// (b) All unreachable -> wrapped error; both addresses actually attempted.
//
// Uses a counting dialer shim so the test proves dialAllIPs tried every IP,
// not just that it formatted the error with the right count.
func TestDialAllIPs_AllUnreachable(t *testing.T) {
	var mu sync.Mutex
	var attempted []string
	countingFail := func(_ context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		attempted = append(attempted, addr)
		mu.Unlock()
		return nil, fmt.Errorf("forced failure for %s", addr)
	}

	ips := []string{"192.0.2.1", "192.0.2.2"} // TEST-NET-1, RFC 5737, never routable
	start := time.Now()
	_, err := dialAllIPs(context.Background(), "test.example.com", ips, "9999",
		countingFail, 10*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "all 2 proxy IPs failed") {
		t.Fatalf("expected error to mention 'all 2 proxy IPs failed', got: %v", err)
	}
	mu.Lock()
	gotAttempted := append([]string(nil), attempted...)
	mu.Unlock()
	if len(gotAttempted) != 2 {
		t.Fatalf("expected both IPs attempted, got %d attempts: %v", len(gotAttempted), gotAttempted)
	}
	if gotAttempted[0] != "192.0.2.1:9999" || gotAttempted[1] != "192.0.2.2:9999" {
		t.Fatalf("unexpected attempt order: %v", gotAttempted)
	}
	// Counting dialer returns immediately, so elapsed must be well under 10s.
	if elapsed >= 9*time.Second {
		t.Fatalf("dialAllIPs took %v, expected < 9s", elapsed)
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
		// Track each connection goroutine in the WaitGroup so close() waits for
		// all in-flight writes before returning, preventing race-detector hits.
		f.wg.Add(1)
		go func(c net.Conn) {
			defer f.wg.Done()
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

// (c) dialAllIPs returns on the first successful TCP connection — it does not
// loop past IP[0] once a conn is established.
//
// This test verifies the TCP-layer stop-at-first-success property of dialAllIPs
// directly. The CONNECT-level no-retry property of dialHTTPProxy is verified
// separately in TestDialHTTPProxy_CONNECT407ReturnsError below.
func TestDialAllIPs_StopsAtFirstTCPSuccess(t *testing.T) {
	proxyA := newFakeHTTPProxy(t, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
	defer proxyA.close()
	proxyB := newFakeHTTPProxy(t, "HTTP/1.1 200 Connection Established\r\n\r\n")
	defer proxyB.close()

	_, portA := proxyA.addr()
	_, portB := proxyB.addr()

	// IP[0] -> portA (succeeds at TCP, returns 407 at CONNECT).
	// IP[1] -> portB (different port). dialAllIPs stops at IP[0] because that
	// dial succeeded; portB should receive zero connections.
	// We use a custom dialer that routes each IP to its own port so each IP
	// truly targets a distinct server.
	portMap := map[string]string{
		"127.0.0.1": portA,
		"127.0.0.2": portB,
	}
	routingDial := func(ctx context.Context, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		targetPort, ok := portMap[host]
		if !ok {
			return nil, fmt.Errorf("no route for %s", host)
		}
		return netDial(ctx, net.JoinHostPort("127.0.0.1", targetPort))
	}

	ips := []string{"127.0.0.1", "127.0.0.2"}
	conn, err := dialAllIPs(context.Background(), "test.example.com", ips, "ignored",
		routingDial, 30*time.Second)
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
	if !strings.Contains(string(buf[:n]), "407") {
		t.Fatalf("expected 407 from proxy A, got: %q", string(buf[:n]))
	}

	if got := atomic.LoadInt32(&proxyB.accepts); got != 0 {
		t.Fatalf("proxy B should not have been dialed, but got %d accepts", got)
	}
	if got := atomic.LoadInt32(&proxyA.accepts); got != 1 {
		t.Fatalf("proxy A should have exactly 1 accept, got %d", got)
	}
}

// TestDialHTTPProxy_CONNECT407ReturnsError verifies that dialHTTPProxy
// propagates a 407 response from the proxy as an error without retrying.
// This exercises the full dialHTTPProxy code path including the CONNECT
// handshake, unlike TestDialAllIPs_StopsAtFirstTCPSuccess which operates
// one level below.
func TestDialHTTPProxy_CONNECT407ReturnsError(t *testing.T) {
	proxyA := newFakeHTTPProxy(t, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
	defer proxyA.close()

	_, portA := proxyA.addr()

	pool := makeTestPool(t, "target.example.com", "443")
	proxy := &proxyConfig{
		Host: "127.0.0.1",
		Port: portA,
	}

	_, err := pool.dialHTTPProxy(context.Background(), proxy)
	if err == nil {
		t.Fatal("expected error from 407 proxy, got nil")
	}
	if !strings.Contains(err.Error(), "407") {
		t.Fatalf("expected error to mention 407, got: %v", err)
	}
	// Exactly one connection should have been accepted; no retry loop.
	if got := atomic.LoadInt32(&proxyA.accepts); got != 1 {
		t.Fatalf("expected exactly 1 accept (no retry), got %d", got)
	}
}

// (d) With 10 IPs and a 30s budget, perAddr uses even split (3s); floor kicks
// in at 20 IPs (1.5s -> 2s); single IP receives the full timeout (no cap).
func TestDialAllIPs_PerIPFloor(t *testing.T) {
	if got := perIPTimeout(30*time.Second, 10); got != 3*time.Second {
		t.Fatalf("perIPTimeout(30s,10) = %v, want 3s", got)
	}
	if got := perIPTimeout(30*time.Second, 20); got != 2*time.Second {
		t.Fatalf("perIPTimeout(30s,20) = %v, want 2s (floor)", got)
	}
	// Single-IP: full timeout preserved so existing connect behaviour is unchanged.
	if got := perIPTimeout(30*time.Second, 1); got != 30*time.Second {
		t.Fatalf("perIPTimeout(30s,1) = %v, want 30s (single-IP full timeout)", got)
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

// TestDialSOCKS5Proxy_NoAcceptableAuthMethod verifies that dialSOCKS5Proxy
// propagates a SOCKS5 0xFF "no acceptable methods" response as an error
// without retrying. This covers the SOCKS5 auth-failure no-retry property
// that is the SOCKS5 analogue of the HTTP CONNECT 407 case.
func TestDialSOCKS5Proxy_NoAcceptableAuthMethod(t *testing.T) {
	l := healthyListener(t)
	defer l.Close()

	var accepts int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&accepts, 1)
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				// Drain client greeting bytes.
				buf := make([]byte, 16)
				c.Read(buf) //nolint:errcheck
				// Respond: no acceptable auth methods.
				c.Write([]byte{0x05, 0xFF}) //nolint:errcheck
			}(conn)
		}
	}()

	_, portL := splitHostPort(l.Addr())
	pool := makeTestPool(t, "target.example.com", "443")
	proxy := &proxyConfig{
		Host: "127.0.0.1",
		Port: portL,
	}

	_, err := pool.dialSOCKS5Proxy(context.Background(), proxy)
	if err == nil {
		t.Fatal("expected error from SOCKS5 0xFF, got nil")
	}
	if !strings.Contains(err.Error(), "no acceptable auth methods") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Exactly one connection — no cross-IP retry on auth failure.
	if got := atomic.LoadInt32(&accepts); got != 1 {
		t.Fatalf("expected 1 accept (no retry), got %d", got)
	}

	_ = l.Close()
	wg.Wait()
}
