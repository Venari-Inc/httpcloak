package transport

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

// stallProxy is a fake HTTP CONNECT proxy that 200s the tunnel then stalls
// forever (never relays), so the client's TLS handshake to the target hangs.
// Mirrors a residential proxy accepting CONNECT but the upstream IP not
// responding -- the case where httpcloak used to ignore the timeout and ride
// to its 30s default.
func stallProxy(t *testing.T) (string, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						c.Close()
						return
					}
					if line == "\r\n" || line == "\n" {
						break
					}
				}
				_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				select {
				case <-done:
				case <-time.After(60 * time.Second):
				}
				c.Close()
			}(c)
		}
	}()
	return ln.Addr().String(), func() { close(done); ln.Close() }
}

// A request through a stalling proxy must abort at the configured timeout, not
// the transport's 30s default, AND a protocol fallback (auto: H2 -> H1) must not
// multiply the budget. Locks the timeout-wiring + shared-deadline fixes.
func TestProxyStallTimeoutBounded(t *testing.T) {
	addr, stop := stallProxy(t)
	defer stop()
	proxyURL := "http://" + addr

	cases := []struct {
		name     string
		protocol Protocol
		setTO    time.Duration // session-level (SetTimeout)
		reqTO    time.Duration // per-request (Request.Timeout)
	}{
		{"h2_session_timeout", ProtocolHTTP2, 2 * time.Second, 0},
		{"h2_request_timeout", ProtocolHTTP2, 0, 2 * time.Second},
		{"auto_cascade", ProtocolAuto, 2 * time.Second, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := NewTransportWithConfig("chrome-146", &ProxyConfig{URL: proxyURL}, nil)
			defer tr.Close()
			tr.SetProtocol(c.protocol)
			if c.setTO > 0 {
				tr.SetTimeout(c.setTO)
			}
			req := &Request{Method: "GET", URL: "https://example.com/", Timeout: c.reqTO}
			t0 := time.Now()
			_, err := tr.Do(context.Background(), req)
			el := time.Since(t0)
			t.Logf("[%s] failed in %.2fs (err: %v)", c.name, el.Seconds(), err)
			if err == nil {
				t.Fatalf("[%s] expected failure through stall proxy", c.name)
			}
			if el > 5*time.Second {
				t.Fatalf("[%s] timeout not honored: 2s budget but rode to %.2fs", c.name, el.Seconds())
			}
		})
	}
}
