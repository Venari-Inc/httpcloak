package transport

import (
	"context"
	"net"
	"testing"
	"time"

	utls "github.com/sardanioss/utls"
)

// blockUntilCancel returns a probe that never connects: it blocks until the
// race context is cancelled, modelling an H3 handshake that idles out because
// QUIC cannot reach the server (firewall, VPN, or a proxy that does not relay
// UDP). This is exactly the case that used to cost ~5s on the sequential path.
func blockUntilCancel() func(context.Context) error {
	return func(c context.Context) error {
		<-c.Done()
		return c.Err()
	}
}

// connectAfter returns a probe that succeeds after d (respecting cancellation).
func connectAfter(d time.Duration) func(context.Context) error {
	return func(c context.Context) error {
		select {
		case <-time.After(d):
			return nil
		case <-c.Done():
			return c.Err()
		}
	}
}

// The whole point of #68: when H3 cannot connect, the racer must fall back to
// H2 as soon as H2 connects, NOT after the full budget. A sequential H3-first
// path would block on the H3 handshake for the budget before trying H2.
func TestRaceTwoProbes_H3BlockedH2Wins_NoStall(t *testing.T) {
	budget := 2 * time.Second
	t0 := time.Now()
	d := raceTwoProbes(context.Background(), budget,
		blockUntilCancel(),               // H3 never connects
		connectAfter(50*time.Millisecond), // H2 connects quickly
	)
	el := time.Since(t0)

	if d.err != nil {
		t.Fatalf("unexpected err: %v", d.err)
	}
	if d.alpnErr != nil {
		t.Fatalf("unexpected alpn mismatch")
	}
	if d.protocol != ProtocolHTTP2 {
		t.Fatalf("want H2 winner, got %v", d.protocol)
	}
	// Must return shortly after H2 connects, well under the budget. Old
	// sequential behaviour would have waited ~budget on H3 first.
	if el > 500*time.Millisecond {
		t.Fatalf("racer stalled: returned in %v (budget %v), expected ~50ms", el, budget)
	}
}

// When H3 connects first it wins, and the racer returns promptly.
func TestRaceTwoProbes_H3Wins(t *testing.T) {
	t0 := time.Now()
	d := raceTwoProbes(context.Background(), 2*time.Second,
		connectAfter(20*time.Millisecond), // H3 fast
		connectAfter(1*time.Second),       // H2 slow
	)
	el := time.Since(t0)
	if d.err != nil || d.alpnErr != nil {
		t.Fatalf("unexpected decision: %+v", d)
	}
	if d.protocol != ProtocolHTTP3 {
		t.Fatalf("want H3 winner, got %v", d.protocol)
	}
	if el > 400*time.Millisecond {
		t.Fatalf("H3 win returned late: %v", el)
	}
}

// When neither connects within the budget, default to H2 so the caller can run
// its H2 -> H1 fallback. Must return at ~budget, not hang.
func TestRaceTwoProbes_BothBlocked_DefaultsH2AtBudget(t *testing.T) {
	budget := 150 * time.Millisecond
	t0 := time.Now()
	d := raceTwoProbes(context.Background(), budget,
		blockUntilCancel(),
		blockUntilCancel(),
	)
	el := time.Since(t0)
	if d.err != nil || d.alpnErr != nil {
		t.Fatalf("unexpected decision: %+v", d)
	}
	if d.protocol != ProtocolHTTP2 {
		t.Fatalf("want H2 default, got %v", d.protocol)
	}
	if el < budget || el > budget+400*time.Millisecond {
		t.Fatalf("expected return near budget %v, got %v", budget, el)
	}
}

// Parent context cancellation surfaces as an error (caller aborts the request).
func TestRaceTwoProbes_ParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	d := raceTwoProbes(ctx, 5*time.Second, blockUntilCancel(), blockUntilCancel())
	if d.err == nil {
		t.Fatalf("expected error on parent cancel, got %+v", d)
	}
}

// An ALPN downgrade to HTTP/1.1 reported by the H2 probe is surfaced (with the
// live TLS conn) so the caller can reuse the connection for H1.
func TestRaceTwoProbes_ALPNDowngradeSurfaced(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	uconn := utls.UClient(clientConn, &utls.Config{InsecureSkipVerify: true}, utls.HelloChrome_Auto)
	alpn := &ALPNMismatchError{TLSConn: uconn}

	d := raceTwoProbes(context.Background(), 2*time.Second,
		blockUntilCancel(), // H3 never connects
		func(c context.Context) error { return alpn }, // H2 negotiated H1
	)
	if d.alpnErr == nil {
		t.Fatalf("expected ALPN mismatch surfaced, got %+v", d)
	}
	if d.alpnErr.TLSConn != uconn {
		t.Fatalf("ALPN conn not preserved for reuse")
	}
	// Caller owns the conn now; close it.
	d.alpnErr.TLSConn.Close()
}
