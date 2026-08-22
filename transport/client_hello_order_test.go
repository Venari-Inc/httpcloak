package transport

import (
	"bufio"
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
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sardanioss/httpcloak/dns"
	"github.com/sardanioss/httpcloak/fingerprint"
	utls "github.com/sardanioss/utls"
)

type capturedClientHello struct {
	extensionOrder []uint16
	cipherSuites   []uint16
	alpn           []string
}

type clientHelloCapture struct {
	hello capturedClientHello
	err   error
}

type helloRecordingConn struct {
	net.Conn
	reader io.Reader
	bytes.Buffer
}

func (c *helloRecordingConn) Read(p []byte) (int, error) {
	reader := c.reader
	if reader == nil {
		reader = c.Conn
	}
	n, err := reader.Read(p)
	if n > 0 {
		_, _ = c.Buffer.Write(p[:n])
	}
	return n, err
}

func (c *helloRecordingConn) Write(p []byte) (int, error) {
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
		DNSNames:              []string{"localhost", "example.com"},
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
	addr      string
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
	s := &clientHelloServer{
		addr:      ln.Addr().String(),
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
	recorded := &helloRecordingConn{Conn: conn}
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

func connectAndCapture(t *testing.T, transport *HTTP2Transport, server *clientHelloServer) capturedClientHello {
	t.Helper()
	host, port, err := net.SplitHostPort(server.addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.createConn(ctx, host, port)
	if err != nil {
		t.Fatalf("createConn: %v", err)
	}
	defer conn.close()
	return server.next(t)
}

func TestClientHelloExtensionOrderVariesPerConnection(t *testing.T) {
	preset := fingerprint.Chrome146Windows()
	if preset.Name != "chrome-146-windows" || preset.JA3 != "" {
		t.Fatalf("test requires the chrome-146-windows ClientHelloID preset, got name=%q JA3=%q", preset.Name, preset.JA3)
	}
	transport := NewHTTP2Transport(preset, dns.NewCache())
	transport.SetInsecureSkipVerify(true)
	defer transport.Close()
	server := startClientHelloServer(t)

	first := connectAndCapture(t, transport, server)
	second := connectAndCapture(t, transport, server)

	if slices.Equal(first.extensionOrder, second.extensionOrder) {
		t.Fatalf("two connections through one transport emitted the same extension order: %v", first.extensionOrder)
	}
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

type fallbackProxy struct {
	url       string
	listener  net.Listener
	captures  chan clientHelloCapture
	tlsConfig *stdtls.Config
}

func startFallbackProxy(t *testing.T) *fallbackProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &fallbackProxy{
		url:       "http://" + ln.Addr().String(),
		listener:  ln,
		captures:  make(chan clientHelloCapture, 2),
		tlsConfig: clientHelloTestTLSConfig(t),
	}
	t.Cleanup(func() {
		speculativeTLSBlocklist.Delete(p.url)
		_ = ln.Close()
	})
	go p.serve()
	return p
}

func (p *fallbackProxy) serve() {
	first, err := p.listener.Accept()
	if err != nil {
		return
	}
	p.captureRejectedSpeculative(first)
	second, err := p.listener.Accept()
	if err != nil {
		return
	}
	p.captureBlockingFallback(second)
}

func readConnectRequest(reader *bufio.Reader) error {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" {
			return nil
		}
	}
}

func readClientHello(reader io.Reader) (capturedClientHello, error) {
	var raw []byte
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(reader, header); err != nil {
			return capturedClientHello{}, err
		}
		payload := make([]byte, int(binary.BigEndian.Uint16(header[3:5])))
		if _, err := io.ReadFull(reader, payload); err != nil {
			return capturedClientHello{}, err
		}
		raw = append(raw, header...)
		raw = append(raw, payload...)
		if hello, err := parseClientHelloRecords(raw); err == nil {
			return hello, nil
		}
	}
}

func (p *fallbackProxy) captureRejectedSpeculative(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if err := readConnectRequest(reader); err != nil {
		p.captures <- clientHelloCapture{err: err}
		return
	}
	hello, err := readClientHello(reader)
	p.captures <- clientHelloCapture{hello: hello, err: err}
	_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
}

func (p *fallbackProxy) captureBlockingFallback(conn net.Conn) {
	reader := bufio.NewReader(conn)
	if err := readConnectRequest(reader); err != nil {
		p.captures <- clientHelloCapture{err: err}
		_ = conn.Close()
		return
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		p.captures <- clientHelloCapture{err: err}
		_ = conn.Close()
		return
	}
	recorded := &helloRecordingConn{Conn: conn, reader: reader}
	tlsConn := stdtls.Server(recorded, p.tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		p.captures <- clientHelloCapture{err: err}
		_ = conn.Close()
		return
	}
	hello, err := parseClientHelloRecords(recorded.Bytes())
	p.captures <- clientHelloCapture{hello: hello, err: err}
	_, _ = io.Copy(io.Discard, tlsConn)
	_ = tlsConn.Close()
}

func (p *fallbackProxy) next(t *testing.T) capturedClientHello {
	t.Helper()
	select {
	case capture := <-p.captures:
		if capture.err != nil {
			t.Fatal(capture.err)
		}
		return capture.hello
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for proxied ClientHello")
		return capturedClientHello{}
	}
}

func connectThroughFallback(t *testing.T, transport *HTTP2Transport, proxy *fallbackProxy, fallbackSpecTransform ...fallbackClientHelloSpecTransform) (capturedClientHello, capturedClientHello) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.createConn(ctx, "example.com", "443", fallbackSpecTransform...)
	if err != nil {
		t.Fatalf("createConn through speculative fallback: %v", err)
	}
	defer conn.close()
	return proxy.next(t), proxy.next(t)
}

func rejectReusedFallbackObjects(initial, fallback *utls.ClientHelloSpec) error {
	if initial == fallback {
		return fmt.Errorf("fallback reused the initial ClientHello spec")
	}
	initialObjects := make(map[uintptr]struct{}, len(initial.Extensions))
	for _, extension := range initial.Extensions {
		value := reflect.ValueOf(extension)
		// Go permits distinct zero-sized objects to share an address.
		if value.Elem().Type().Size() != 0 {
			initialObjects[value.Pointer()] = struct{}{}
		}
	}
	for _, extension := range fallback.Extensions {
		if _, reused := initialObjects[reflect.ValueOf(extension).Pointer()]; reused {
			return fmt.Errorf("fallback reused initial extension object %T", extension)
		}
	}
	return nil
}

func requireEquivalentClientHellos(t *testing.T, initial, rebuild capturedClientHello) {
	t.Helper()
	if !slices.Equal(initial.extensionOrder, rebuild.extensionOrder) {
		t.Fatalf("one connection changed extension order across its rebuild: %v vs %v", initial.extensionOrder, rebuild.extensionOrder)
	}
	if !slices.Equal(extensionSet(initial.extensionOrder), extensionSet(rebuild.extensionOrder)) {
		t.Fatalf("one connection changed extension set across its rebuild: %v vs %v", extensionSet(initial.extensionOrder), extensionSet(rebuild.extensionOrder))
	}
	if !slices.Equal(initial.cipherSuites, rebuild.cipherSuites) {
		t.Fatalf("one connection changed cipher list across its rebuild: %v vs %v", initial.cipherSuites, rebuild.cipherSuites)
	}
	if !slices.Equal(initial.alpn, rebuild.alpn) {
		t.Fatalf("one connection changed ALPN across its rebuild: %v vs %v", initial.alpn, rebuild.alpn)
	}
}

func clientHelloIDCipherSuites(t *testing.T, id utls.ClientHelloID, seed int64) []uint16 {
	t.Helper()
	spec, err := utls.UTLSIdToSpecWithSeed(id, seed)
	if err != nil {
		t.Fatalf("generate expected ClientHello spec for %q: %v", id.Client, err)
	}
	ciphers := slices.Clone(spec.CipherSuites)
	for i := range ciphers {
		ciphers[i] = normalizeGREASE(ciphers[i])
	}
	return ciphers
}

func TestClientHelloExtensionOrderStableWithinConnectionRebuild(t *testing.T) {
	proxy := startFallbackProxy(t)
	preset := fingerprint.Chrome146Windows()
	if preset.Name != "chrome-146-windows" || preset.JA3 != "" || preset.PSKClientHelloID.Client == "" {
		t.Fatalf("test requires the chrome-146-windows PSK ClientHelloID arm, got name=%q JA3=%q PSK client=%q", preset.Name, preset.JA3, preset.PSKClientHelloID.Client)
	}
	transport := NewHTTP2TransportWithConfig(
		preset,
		dns.NewCache(),
		&ProxyConfig{URL: proxy.url},
		&TransportConfig{EnableSpeculativeTLS: true},
	)
	if !transport.hasPSKSpec {
		t.Fatal("test transport did not resolve through the PSK ClientHelloID arm")
	}
	transport.SetInsecureSkipVerify(true)
	defer transport.Close()

	initial, rebuild := connectThroughFallback(t, transport, proxy, rejectReusedFallbackObjects)
	requireEquivalentClientHellos(t, initial, rebuild)
}

func TestClientHelloSpecSourcePrecedenceSurvivesRebuild(t *testing.T) {
	const customJA3 = "771,4865,0-10-11-13-16-43-45-51,29-23,0"
	const presetJA3 = "771,4866,0-10-11-13-16-43-45-51,29-23,0"
	extras := &fingerprint.JA3Extras{ALPN: []string{"h2"}}

	tests := []struct {
		name         string
		configure    func(*fingerprint.Preset, *TransportConfig)
		wantPSKArm   bool
		wantCiphers  []uint16
		wantSourceID func(*fingerprint.Preset) utls.ClientHelloID
	}{
		{
			name: "CustomJA3 beats preset JA3",
			configure: func(preset *fingerprint.Preset, config *TransportConfig) {
				preset.JA3 = presetJA3
				preset.JA3Extras = extras
				config.CustomJA3 = customJA3
				config.CustomJA3Extras = extras
			},
			wantCiphers: []uint16{4865},
		},
		{
			name: "preset JA3 beats ID arms",
			configure: func(preset *fingerprint.Preset, _ *TransportConfig) {
				preset.JA3 = presetJA3
				preset.JA3Extras = extras
			},
			wantCiphers: []uint16{4866},
		},
		{
			name: "PSK ID beats regular ID",
			configure: func(preset *fingerprint.Preset, _ *TransportConfig) {
				preset.PSKClientHelloID = utls.HelloFirefox_120
			},
			wantPSKArm: true,
			wantSourceID: func(preset *fingerprint.Preset) utls.ClientHelloID {
				return preset.PSKClientHelloID
			},
		},
		{
			name: "regular ID when PSK ID is absent",
			configure: func(preset *fingerprint.Preset, _ *TransportConfig) {
				preset.PSKClientHelloID = utls.ClientHelloID{}
			},
			wantSourceID: func(preset *fingerprint.Preset) utls.ClientHelloID {
				return preset.ClientHelloID
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxy := startFallbackProxy(t)
			preset := fingerprint.Chrome146Windows()
			config := &TransportConfig{EnableSpeculativeTLS: true}
			test.configure(preset, config)
			transport := NewHTTP2TransportWithConfig(
				preset,
				dns.NewCache(),
				&ProxyConfig{URL: proxy.url},
				config,
			)
			if transport.hasPSKSpec != test.wantPSKArm && preset.JA3 == "" && config.CustomJA3 == "" {
				t.Fatalf("hasPSKSpec = %v, want %v", transport.hasPSKSpec, test.wantPSKArm)
			}
			transport.SetInsecureSkipVerify(true)
			defer transport.Close()

			initial, rebuild := connectThroughFallback(t, transport, proxy)
			requireEquivalentClientHellos(t, initial, rebuild)
			if test.wantCiphers != nil && !slices.Equal(initial.cipherSuites, test.wantCiphers) {
				t.Fatalf("cipher list %v, want %v", initial.cipherSuites, test.wantCiphers)
			}
			if test.wantSourceID != nil {
				wantCiphers := clientHelloIDCipherSuites(t, test.wantSourceID(preset), transport.shuffleSeed)
				if !slices.Equal(initial.cipherSuites, wantCiphers) {
					t.Fatalf("cipher list %v does not come from selected ClientHello ID %q: want %v", initial.cipherSuites, test.wantSourceID(preset).Client, wantCiphers)
				}
			}
		})
	}
}

func TestReorderClientHelloExtensionsHandlesDuplicateKeys(t *testing.T) {
	initial := []utls.TLSExtension{
		&utls.UtlsGREASEExtension{},
		&utls.GenericExtension{Id: 1234},
		&utls.SNIExtension{},
		&utls.GenericExtension{Id: 5678},
		&utls.UtlsGREASEExtension{},
	}
	order := captureClientHelloExtensionOrder(initial)

	firstGREASE := &utls.UtlsGREASEExtension{}
	secondGREASE := &utls.UtlsGREASEExtension{}
	firstGeneric := &utls.GenericExtension{Id: 1234}
	secondGeneric := &utls.GenericExtension{Id: 5678}
	sni := &utls.SNIExtension{}
	fallback := []utls.TLSExtension{
		firstGREASE,
		secondGeneric,
		sni,
		firstGeneric,
		secondGREASE,
	}

	if err := reorderClientHelloExtensions(fallback, order); err != nil {
		t.Fatalf("reorderClientHelloExtensions: %v", err)
	}
	want := []utls.TLSExtension{firstGREASE, firstGeneric, sni, secondGeneric, secondGREASE}
	for i := range want {
		if fallback[i] != want[i] {
			t.Fatalf("extension %d = %T %p, want %T %p", i, fallback[i], fallback[i], want[i], want[i])
		}
	}
}

func TestReorderClientHelloExtensionsRejectsMismatchedSet(t *testing.T) {
	order := captureClientHelloExtensionOrder([]utls.TLSExtension{
		&utls.SNIExtension{},
		&utls.ALPNExtension{},
	})
	tests := []struct {
		name       string
		extensions []utls.TLSExtension
	}{
		{name: "missing", extensions: []utls.TLSExtension{&utls.SNIExtension{}}},
		{name: "extra", extensions: []utls.TLSExtension{&utls.SNIExtension{}, &utls.ALPNExtension{}, &utls.StatusRequestExtension{}}},
		{name: "substituted", extensions: []utls.TLSExtension{&utls.SNIExtension{}, &utls.StatusRequestExtension{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := reorderClientHelloExtensions(test.extensions, order); err == nil {
				t.Fatal("reorderClientHelloExtensions succeeded with a mismatched extension set")
			}
		})
	}
}

func TestClientHelloFallbackReorderErrorPropagates(t *testing.T) {
	proxy := startFallbackProxy(t)
	preset := fingerprint.Chrome146Windows()
	transport := NewHTTP2TransportWithConfig(
		preset,
		dns.NewCache(),
		&ProxyConfig{URL: proxy.url},
		&TransportConfig{EnableSpeculativeTLS: true},
	)
	transport.SetInsecureSkipVerify(true)
	defer transport.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.establishConn(ctx, "example.com", "443", false, func(_ *utls.ClientHelloSpec, spec *utls.ClientHelloSpec) error {
		for i, extension := range spec.Extensions {
			if _, ok := extension.(*utls.SCTExtension); ok {
				spec.Extensions = append(spec.Extensions[:i], spec.Extensions[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("test fallback spec has no SCT extension to remove")
	})
	if conn != nil {
		conn.close()
		t.Fatal("establishConn returned a connection after the fallback extension set changed")
	}
	if err == nil || !strings.Contains(err.Error(), "extension count changed") {
		t.Fatalf("establishConn error = %v, want propagated fallback extension-set error", err)
	}
}
