package transport

import (
	"fmt"
	"slices"
	"testing"

	"github.com/sardanioss/httpcloak/dns"
	"github.com/sardanioss/httpcloak/fingerprint"
	utls "github.com/sardanioss/utls"
)

// deterministicBaseSpecID is a ClientHelloID whose uTLS spec-table entry is not
// wrapped in ShuffleChromeTLSExtensions, so its extension order is decided
// solely by the seed handed to UTLSIdToSpecWithSeed.
//
// This matters because the Chrome >= 110 entries re-shuffle their base spec on
// every generation with an unseeded CSPRNG. That masks the seed's lifetime: a
// transport-scoped seed still yields a different order per connection, so a
// test driven from a modern Chrome ID cannot observe whether the seed is drawn
// per transport or per connection. This ID can.
var deterministicBaseSpecID = utls.HelloChrome_102

func specExtensionOrder(t *testing.T, id utls.ClientHelloID, seed int64) string {
	t.Helper()
	spec, err := utls.UTLSIdToSpecWithSeed(id, seed)
	if err != nil {
		t.Fatalf("generate spec for %q: %v", id.Client, err)
	}
	order := ""
	for _, ext := range spec.Extensions {
		order += fmt.Sprintf("%T|", ext)
	}
	return order
}

// TestClientHelloShuffleSeedIsDrawnPerConnection pins the lifetime of the
// ClientHello shuffle seed at the wire boundary: two connections opened through
// ONE transport must present different extension orders.
//
// Chrome has permuted its ClientHello extension order on every connection since
// Chrome 110 (anti-ossification). A seed drawn once per transport reproduces one
// order for the life of that transport, which no Chrome since 110 has done.
func TestClientHelloShuffleSeedIsDrawnPerConnection(t *testing.T) {
	// If this ever fails, uTLS started shuffling this ID's base spec too and the
	// test can no longer observe the seed's lifetime — pick another ClientHelloID
	// whose spec-table entry has a plain Extensions slice.
	if a, b := specExtensionOrder(t, deterministicBaseSpecID, 4242), specExtensionOrder(t, deterministicBaseSpecID, 4242); a != b {
		t.Fatalf("ClientHelloID %q no longer has a deterministic base spec, so this test cannot observe the seed's lifetime; pick another ID", deterministicBaseSpecID.Client)
	}

	preset := fingerprint.Chrome146Windows()
	preset.ClientHelloID = deterministicBaseSpecID
	preset.PSKClientHelloID = utls.ClientHelloID{}
	if preset.JA3 != "" {
		t.Fatalf("test requires a ClientHelloID preset with no JA3 string, got JA3=%q", preset.JA3)
	}

	transport := NewHTTP2Transport(preset, dns.NewCache())
	transport.SetInsecureSkipVerify(true)
	defer transport.Close()
	if transport.hasPSKSpec {
		t.Fatal("test transport must resolve through the regular ClientHelloID arm, not the PSK arm")
	}
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
