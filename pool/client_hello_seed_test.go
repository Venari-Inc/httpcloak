package pool

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	stdtls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/sardanioss/httpcloak/dns"
	"github.com/sardanioss/httpcloak/fingerprint"
	utls "github.com/sardanioss/utls"
)

// deterministicBaseSpecID is a ClientHelloID whose uTLS spec-table entry is not
// wrapped in ShuffleChromeTLSExtensions, so its extension order is decided
// solely by the seed handed to the spec builder.
//
// The Chrome >= 110 entries (chrome-146-windows included) re-shuffle their base
// spec on every generation with an unseeded CSPRNG. That masks the seed's
// lifetime: a pool-scoped seed still yields a different order per connection, so
// a test driven from a modern Chrome ID cannot observe whether the seed is drawn
// per pool or per connection. This ID can.
var deterministicBaseSpecID = utls.HelloChrome_102

type capturedClientHello struct {
	extensionOrder []uint16
	cipherSuites   []uint16
	alpn           []string
}

type clientHelloCapture struct {
	hello capturedClientHello
	err   error
}

type recordingConn struct {
	net.Conn
	bytes.Buffer
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		_, _ = c.Buffer.Write(p[:n])
	}
	return n, err
}

func (c *recordingConn) Write(p []byte) (int, error) {
	return c.Conn.Write(p)
}

func clientHelloTestTLSConfig(t *testing.T) *stdtls.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	return &stdtls.Config{
		Certificates:           []stdtls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:             []string{"h2"},
		MinVersion:             stdtls.VersionTLS12,
		SessionTicketsDisabled: true,
	}
}

type clientHelloServer struct {
	host      string
	port      string
	listener  net.Listener
	captures  chan clientHelloCapture
	tlsConfig *stdtls.Config
}

func startClientHelloServer(t *testing.T) *clientHelloServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, port := splitHostPort(ln.Addr())
	s := &clientHelloServer{
		host:      host,
		port:      port,
		listener:  ln,
		captures:  make(chan clientHelloCapture, 8),
		tlsConfig: clientHelloTestTLSConfig(t),
	}
	t.Cleanup(func() { _ = ln.Close() })
	go s.acceptLoop()
	return s
}

func (s *clientHelloServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *clientHelloServer) handleConn(conn net.Conn) {
	recorded := &recordingConn{Conn: conn}
	tlsConn := stdtls.Server(recorded, s.tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		s.captures <- clientHelloCapture{err: fmt.Errorf("server handshake: %w", err)}
		_ = conn.Close()
		return
	}
	hello, err := parseClientHelloRecords(recorded.Bytes())
	s.captures <- clientHelloCapture{hello: hello, err: err}
	_, _ = io.Copy(io.Discard, tlsConn)
	_ = tlsConn.Close()
}

func (s *clientHelloServer) next(t *testing.T) capturedClientHello {
	t.Helper()
	select {
	case capture := <-s.captures:
		if capture.err != nil {
			t.Fatal(capture.err)
		}
		return capture.hello
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ClientHello")
		return capturedClientHello{}
	}
}

func parseClientHelloRecords(raw []byte) (capturedClientHello, error) {
	var handshake []byte
	for len(raw) >= 5 {
		recordLen := int(binary.BigEndian.Uint16(raw[3:5]))
		if len(raw) < 5+recordLen {
			break
		}
		if raw[0] == 22 {
			handshake = append(handshake, raw[5:5+recordLen]...)
			if len(handshake) >= 4 {
				messageLen := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
				if len(handshake) >= 4+messageLen {
					if handshake[0] != 1 {
						return capturedClientHello{}, fmt.Errorf("first handshake message type = %d, want ClientHello", handshake[0])
					}
					return parseClientHello(handshake[4 : 4+messageLen])
				}
			}
		}
		raw = raw[5+recordLen:]
	}
	return capturedClientHello{}, fmt.Errorf("complete ClientHello not found")
}

func parseClientHello(body []byte) (capturedClientHello, error) {
	if len(body) < 35 {
		return capturedClientHello{}, fmt.Errorf("ClientHello too short: %d", len(body))
	}
	pos := 34 // legacy_version + random
	sessionIDLen := int(body[pos])
	pos++
	if pos+sessionIDLen+2 > len(body) {
		return capturedClientHello{}, fmt.Errorf("invalid session id length")
	}
	pos += sessionIDLen
	cipherLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if cipherLen%2 != 0 || pos+cipherLen+1 > len(body) {
		return capturedClientHello{}, fmt.Errorf("invalid cipher suite length")
	}
	ciphers := make([]uint16, 0, cipherLen/2)
	for end := pos + cipherLen; pos < end; pos += 2 {
		ciphers = append(ciphers, normalizeGREASE(binary.BigEndian.Uint16(body[pos:pos+2])))
	}
	compressionLen := int(body[pos])
	pos++
	if pos+compressionLen+2 > len(body) {
		return capturedClientHello{}, fmt.Errorf("invalid compression methods length")
	}
	pos += compressionLen
	extensionsLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if pos+extensionsLen > len(body) {
		return capturedClientHello{}, fmt.Errorf("invalid extensions length")
	}

	hello := capturedClientHello{cipherSuites: ciphers}
	for end := pos + extensionsLen; pos < end; {
		if pos+4 > end {
			return capturedClientHello{}, fmt.Errorf("truncated extension header")
		}
		extensionID := binary.BigEndian.Uint16(body[pos : pos+2])
		extensionLen := int(binary.BigEndian.Uint16(body[pos+2 : pos+4]))
		pos += 4
		if pos+extensionLen > end {
			return capturedClientHello{}, fmt.Errorf("truncated extension %d", extensionID)
		}
		hello.extensionOrder = append(hello.extensionOrder, normalizeGREASE(extensionID))
		if extensionID == 16 {
			alpn, err := parseALPN(body[pos : pos+extensionLen])
			if err != nil {
				return capturedClientHello{}, err
			}
			hello.alpn = alpn
		}
		pos += extensionLen
	}
	return hello, nil
}

func parseALPN(data []byte) ([]string, error) {
	if len(data) < 2 || int(binary.BigEndian.Uint16(data[:2])) != len(data)-2 {
		return nil, fmt.Errorf("invalid ALPN extension")
	}
	var protocols []string
	for pos := 2; pos < len(data); {
		length := int(data[pos])
		pos++
		if pos+length > len(data) {
			return nil, fmt.Errorf("invalid ALPN protocol length")
		}
		protocols = append(protocols, string(data[pos:pos+length]))
		pos += length
	}
	return protocols, nil
}

func normalizeGREASE(value uint16) uint16 {
	if byte(value>>8) == byte(value) && value&0x0f0f == 0x0a0a {
		return 0x0a0a
	}
	return value
}

func extensionSet(order []uint16) []uint16 {
	set := slices.Clone(order)
	sort.Slice(set, func(i, j int) bool { return set[i] < set[j] })
	return set
}

// deterministicSpecPreset returns a preset that resolves through the regular
// ClientHelloID arm with a spec whose extension order is decided by the seed
// alone, so the seed's lifetime is observable at the wire.
func deterministicSpecPreset(t *testing.T) *fingerprint.Preset {
	t.Helper()
	if a, b := seedExtensionOrder(t, deterministicBaseSpecID, 4242), seedExtensionOrder(t, deterministicBaseSpecID, 4242); a != b {
		t.Fatalf("ClientHelloID %q no longer has a deterministic base spec, so this test cannot observe the seed's lifetime; pick another ID", deterministicBaseSpecID.Client)
	}
	preset := fingerprint.Chrome146Windows()
	if preset.JA3 != "" {
		t.Fatalf("test requires a ClientHelloID preset with no JA3 string, got JA3=%q", preset.JA3)
	}
	preset.ClientHelloID = deterministicBaseSpecID
	preset.PSKClientHelloID = utls.ClientHelloID{}
	return preset
}

func seedExtensionOrder(t *testing.T, id utls.ClientHelloID, seed int64) string {
	t.Helper()
	spec, err := utls.UTLSIdToSpecWithSeed(id, seed)
	if err != nil {
		t.Fatalf("generate spec for %q: %v", id.Client, err)
	}
	order := ""
	for _, extension := range spec.Extensions {
		order += fmt.Sprintf("%T|", extension)
	}
	return order
}

// newSeedTestPool builds a HostPool aimed at server whose reference specs were
// generated at pool-construction time from cachedSpecSeed.
func newSeedTestPool(t *testing.T, preset *fingerprint.Preset, server *clientHelloServer, cachedSpecSeed int64) *HostPool {
	t.Helper()
	var cachedSpec, cachedPSKSpec *utls.ClientHelloSpec
	if spec, err := tcpClientHelloSpec(preset, preset.ClientHelloID, cachedSpecSeed); err == nil {
		cachedSpec = spec
	} else {
		t.Fatalf("build reference spec: %v", err)
	}
	if preset.PSKClientHelloID.Client != "" {
		if spec, err := tcpClientHelloSpec(preset, preset.PSKClientHelloID, cachedSpecSeed); err == nil {
			cachedPSKSpec = spec
		}
	}
	pool := NewHostPoolWithConfig(server.host, "", server.port, preset, dns.NewCache(), true, "", cachedSpec, cachedPSKSpec, nil)
	t.Cleanup(pool.Close)
	return pool
}

func poolConnectAndCapture(t *testing.T, pool *HostPool, server *clientHelloServer) capturedClientHello {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pool.createConn(ctx)
	if err != nil {
		t.Fatalf("createConn: %v", err)
	}
	defer conn.Close()
	return server.next(t)
}

func requireSameClientHelloShape(t *testing.T, first, second capturedClientHello) {
	t.Helper()
	if !slices.Equal(extensionSet(first.extensionOrder), extensionSet(second.extensionOrder)) {
		t.Fatalf("extension set changed: %v vs %v", extensionSet(first.extensionOrder), extensionSet(second.extensionOrder))
	}
	if !slices.Equal(first.cipherSuites, second.cipherSuites) {
		t.Fatalf("cipher list changed: %v vs %v", first.cipherSuites, second.cipherSuites)
	}
	if !slices.Equal(first.alpn, second.alpn) {
		t.Fatalf("ALPN changed: %v vs %v", first.alpn, second.alpn)
	}
}

// TestHostPoolClientHelloShuffleSeedIsDrawnPerConnection pins the lifetime of
// the ClientHello shuffle seed at the wire boundary of the pool path: two
// connections created by ONE HostPool must present different extension orders.
//
// Chrome has permuted its ClientHello extension order on every connection since
// Chrome 110 (anti-ossification). A seed drawn once per pool - or once per pool
// manager and handed down - reproduces one order for every connection under it,
// which no Chrome since 110 has done.
func TestHostPoolClientHelloShuffleSeedIsDrawnPerConnection(t *testing.T) {
	server := startClientHelloServer(t)
	pool := newSeedTestPool(t, deterministicSpecPreset(t), server, 4242)

	first := poolConnectAndCapture(t, pool, server)
	second := poolConnectAndCapture(t, pool, server)

	if slices.Equal(first.extensionOrder, second.extensionOrder) {
		t.Fatalf("two connections from one HostPool emitted the same extension order: %v", first.extensionOrder)
	}
	requireSameClientHelloShape(t, first, second)
}

// TestHostPoolCachedSpecIsNotReusedForTheNextConnection pins the precedence
// rule: the spec built for THIS connection wins over the spec cached at pool
// construction. Both pools here are built from the same cached reference spec
// and the same construction-time seed - exactly what PoolManager hands every
// host pool it creates - so if that cached spec's seed reached the wire, every
// connection under it would replay one extension order.
func TestHostPoolCachedSpecIsNotReusedForTheNextConnection(t *testing.T) {
	const cachedSpecSeed = int64(4242)
	server := startClientHelloServer(t)
	preset := deterministicSpecPreset(t)
	poolA := newSeedTestPool(t, preset, server, cachedSpecSeed)
	poolB := newSeedTestPool(t, preset, server, cachedSpecSeed)

	captures := map[string]capturedClientHello{
		"pool A connection 1": poolConnectAndCapture(t, poolA, server),
		"pool A connection 2": poolConnectAndCapture(t, poolA, server),
		"pool B connection 1": poolConnectAndCapture(t, poolB, server),
	}
	names := []string{"pool A connection 1", "pool A connection 2", "pool B connection 1"}
	for i, left := range names {
		for _, right := range names[i+1:] {
			if slices.Equal(captures[left].extensionOrder, captures[right].extensionOrder) {
				t.Fatalf("%s and %s replayed one extension order from the spec cached at construction (seed %d): %v",
					left, right, cachedSpecSeed, captures[left].extensionOrder)
			}
			requireSameClientHelloShape(t, captures[left], captures[right])
		}
	}
}

// TestHostPoolDialFailureDoesNotPinTheNextConnectionsOrder covers failure mode
// E1: a connection that fails before its handshake must not leave a seed behind
// for the connections that follow it.
func TestHostPoolDialFailureDoesNotPinTheNextConnectionsOrder(t *testing.T) {
	server := startClientHelloServer(t)
	pool := newSeedTestPool(t, deterministicSpecPreset(t), server, 4242)

	deadHost, deadPort := deadAddr(t)
	pool.host, pool.port = deadHost, deadPort
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := pool.createConn(ctx); err == nil {
		cancel()
		t.Fatal("createConn against a refused address unexpectedly succeeded")
	}
	cancel()

	pool.host, pool.port = server.host, server.port
	first := poolConnectAndCapture(t, pool, server)
	second := poolConnectAndCapture(t, pool, server)

	if slices.Equal(first.extensionOrder, second.extensionOrder) {
		t.Fatalf("connections following a failed dial emitted the same extension order: %v", first.extensionOrder)
	}
	requireSameClientHelloShape(t, first, second)
}
