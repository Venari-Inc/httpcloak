package pool

import (
	"context"
	"testing"

	"github.com/sardanioss/httpcloak/dns"
	"github.com/sardanioss/httpcloak/fingerprint"
	utls "github.com/sardanioss/utls"
)

// deterministicQUICPreset returns the QUIC preset used by these tests after
// asserting the two properties they depend on: its QUIC ClientHelloID derives a
// deterministic spec from a seed (otherwise the seed's lifetime is not
// observable at all), and it carries no signature-algorithm override (so the
// expected order can be derived straight from utls rather than from the pool
// code under test).
func deterministicQUICPreset(t *testing.T) *fingerprint.Preset {
	t.Helper()
	preset := fingerprint.Chrome146Windows()
	id := preset.QUICClientHelloID
	if id.Client == "" {
		t.Fatal("preset has no QUIC ClientHelloID, so the QUIC ClientHello path cannot be exercised")
	}
	if len(preset.QUICSignatureAlgorithms) != 0 {
		t.Fatalf("preset overrides QUIC signature algorithms (%d), so utls alone no longer derives the expected order", len(preset.QUICSignatureAlgorithms))
	}
	if a, b := seedExtensionOrder(t, id, 4242), seedExtensionOrder(t, id, 4242); a != b {
		t.Fatalf("QUIC ClientHelloID %q no longer has a deterministic base spec, so this test cannot observe the seed's lifetime", id.Client)
	}
	return preset
}

// newSeedTestQUICPool builds a QUIC host pool the way the QUIC manager does:
// with reference specs and a seed drawn once, above the pool, and shared by
// every pool the manager hands out.
func newSeedTestQUICPool(t *testing.T, preset *fingerprint.Preset, cachedSpecSeed int64) *QUICHostPool {
	t.Helper()
	var cachedSpec, cachedPSKSpec *utls.ClientHelloSpec
	if spec, err := quicClientHelloSpec(preset, preset.QUICClientHelloID, cachedSpecSeed); err == nil {
		cachedSpec = spec
	}
	if preset.QUICPSKClientHelloID.Client != "" {
		if spec, err := quicClientHelloSpec(preset, preset.QUICPSKClientHelloID, cachedSpecSeed); err == nil {
			cachedPSKSpec = spec
		}
	}
	pool := NewQUICHostPoolWithCachedSpec("127.0.0.1", "", "443", preset, dns.NewCache(), cachedSpec, cachedPSKSpec)
	pool.disableECH = true
	t.Cleanup(pool.Close)
	return pool
}

// quicConnClientHello returns the extension order and shuffle seed the pool put
// on a freshly created QUIC connection. createConn performs no dial, so the
// values it hands quic-go are readable without a QUIC server.
func quicConnClientHello(t *testing.T, pool *QUICHostPool) (order string, seed int64) {
	t.Helper()
	conn, err := pool.createConn(context.Background())
	if err != nil {
		t.Fatalf("create QUIC connection: %v", err)
	}
	cfg := conn.HTTP3RT.QUICConfig
	if cfg == nil {
		t.Fatal("QUIC connection carries no quic.Config")
	}
	if cfg.CachedClientHelloSpec == nil {
		t.Fatal("QUIC connection carries no ClientHelloSpec, so nothing pins its extension order")
	}
	return specExtensionOrder(cfg.CachedClientHelloSpec), cfg.TransportParameterShuffleSeed
}

// TestQUICHostPoolClientHelloShuffleSeedIsDrawnPerConnection pins that two QUIC
// connections from one pool do not replay one extension order. Chrome permutes
// its ClientHello extension order on every connection, so a seed shared by the
// pool (or by the manager above it) is a fingerprint no Chrome since 110 emits.
func TestQUICHostPoolClientHelloShuffleSeedIsDrawnPerConnection(t *testing.T) {
	preset := deterministicQUICPreset(t)
	pool := newSeedTestQUICPool(t, preset, 4242)

	firstOrder, firstSeed := quicConnClientHello(t, pool)
	secondOrder, secondSeed := quicConnClientHello(t, pool)

	if firstSeed == secondSeed {
		t.Fatalf("both QUIC connections from one pool were built from shuffle seed %d, so the seed outlives the connection", firstSeed)
	}
	if firstOrder == secondOrder {
		t.Fatalf("both QUIC connections sent the same ClientHello extension order:\n%s", firstOrder)
	}
}

// TestQUICHostPoolCachedSpecIsNotReusedForTheNextConnection pins the precedence
// rule: the reference spec the manager builds once and hands to every pool is
// an availability record, never wire material. Two pools built from the same
// reference spec must still differ from each other and from themselves.
func TestQUICHostPoolCachedSpecIsNotReusedForTheNextConnection(t *testing.T) {
	preset := deterministicQUICPreset(t)
	const sharedSeed int64 = 4242
	poolA := newSeedTestQUICPool(t, preset, sharedSeed)
	poolB := newSeedTestQUICPool(t, preset, sharedSeed)

	firstA, _ := quicConnClientHello(t, poolA)
	secondA, _ := quicConnClientHello(t, poolA)
	firstB, _ := quicConnClientHello(t, poolB)

	orders := map[string]string{
		"pool A connection 1": firstA,
		"pool A connection 2": secondA,
		"pool B connection 1": firstB,
	}
	seen := map[string]string{}
	for name, order := range orders {
		if other, dup := seen[order]; dup {
			t.Fatalf("%s and %s sent the same ClientHello extension order, so the shared reference spec's seed reached the wire:\n%s", other, name, order)
		}
		seen[order] = name
	}
}

// TestQUICHostPoolConnectionUsesOneSeedForItsWholeClientHello is the inverse
// case: the seed varies between connections and must not vary within one. The
// spec a connection carries must be the one utls derives from that same
// connection's transport-parameter shuffle seed — one seed, one connection,
// both halves of the fingerprint.
func TestQUICHostPoolConnectionUsesOneSeedForItsWholeClientHello(t *testing.T) {
	preset := deterministicQUICPreset(t)
	pool := newSeedTestQUICPool(t, preset, 4242)

	order, seed := quicConnClientHello(t, pool)
	want := seedExtensionOrder(t, preset.QUICClientHelloID, seed)
	if order != want {
		t.Fatalf("the connection's ClientHello order is not the one utls derives from that connection's own shuffle seed %d:\n got: %s\nwant: %s", seed, order, want)
	}
}
