package transport

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// recordingSocks5 is a minimal SOCKS5 proxy that completes the no-auth
// handshake and a UDP ASSOCIATE, then records whether ANY UDP datagram reaches
// its relay socket. It never forwards the datagram, so a QUIC handshake through
// it will not complete. The point is the side effect: a datagram arriving at
// the relay proves the client tunneled its QUIC probe through the proxy instead
// of dialing the target directly (which would be a real-IP leak).
func recordingSocks5(t *testing.T) (addr string, gotDatagram func() bool, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	relayPort := udp.LocalAddr().(*net.UDPAddr).Port

	var recvd atomic.Bool
	done := make(chan struct{})

	// Relay read loop: flag the first datagram, then drop it.
	go func() {
		buf := make([]byte, 4096)
		for {
			_ = udp.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			if n, _, err := udp.ReadFromUDP(buf); err == nil && n > 0 {
				recvd.Store(true)
			}
			select {
			case <-done:
				return
			default:
			}
		}
	}()

	// SOCKS5 control loop.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Greeting: VER, NMETHODS, METHODS...
				g := make([]byte, 2)
				if _, err := io.ReadFull(c, g); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, int(g[1]))); err != nil {
					return
				}
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil { // no auth
					return
				}
				// Request: VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT
				h := make([]byte, 4)
				if _, err := io.ReadFull(c, h); err != nil {
					return
				}
				switch h[3] {
				case 0x01:
					io.ReadFull(c, make([]byte, 4+2))
				case 0x04:
					io.ReadFull(c, make([]byte, 16+2))
				case 0x03:
					l := make([]byte, 1)
					io.ReadFull(c, l)
					io.ReadFull(c, make([]byte, int(l[0])+2))
				}
				// Reply success, pointing the client at our UDP relay.
				_, _ = c.Write([]byte{
					0x05, 0x00, 0x00, 0x01,
					127, 0, 0, 1,
					byte(relayPort >> 8), byte(relayPort),
				})
				<-done // hold the control conn open so the tunnel stays up
			}(c)
		}
	}()

	return ln.Addr().String(), recvd.Load, func() {
		close(done)
		ln.Close()
		udp.Close()
	}
}

// Locks the no-real-IP-leak invariant for the proxied H3 connection probe.
// HTTP3Transport.Connect() used to dial quic.DialAddr() straight to the target,
// which over a proxy would bypass the SOCKS5 relay entirely. The fix routes the
// probe through the same proxy dial path real requests use. With a SOCKS5 proxy
// configured, the QUIC Initial must reach the proxy relay, never the target.
func TestH3ConnectTunnelsThroughProxy_NoLeak(t *testing.T) {
	proxyAddr, gotDatagram, stop := recordingSocks5(t)
	defer stop()

	tr := NewTransportWithConfig("chrome-146", &ProxyConfig{URL: "socks5://" + proxyAddr}, nil)
	defer tr.Close()
	if tr.h3Transport == nil {
		t.Skip("no h3Transport constructed for socks5 proxy; nothing to probe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// The relay black-holes traffic, so the QUIC handshake cannot complete and
	// Connect returns an error. We assert only the side effect.
	_ = tr.h3Transport.Connect(ctx, "leak-canary.test", "443")

	if !gotDatagram() {
		t.Fatal("no datagram reached the proxy relay: H3 Connect did not tunnel through the proxy (real-IP leak)")
	}
}
