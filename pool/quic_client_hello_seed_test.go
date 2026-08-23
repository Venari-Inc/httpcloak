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

// quicConnClientHello returns the extension order and transport-parameter
// shuffle seed the pool put on a freshly created QUIC connection. createConn
// performs no dial, so the values it hands quic-go are readable without a QUIC
// server.
func quicConnClientHello(t *testing.T, pool *QUICHostPool) (order string, transportParamSeed int64) {
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

	firstOrder, _ := quicConnClientHello(t, pool)
	secondOrder, _ := quicConnClientHello(t, pool)

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

// TestQUICHostPoolLeavesTransportParameterOrderUnseeded pins that the
// ClientHello seed is not also handed to quic-go as the transport-parameter
// seed. h3build leaves that seed at zero so quic-go reshuffles the parameters
// on every serialization; the two orders are independent by design (see
// h3build.QUICOptions), and a non-zero seed here would freeze the parameter
// order for the life of whatever holds it.
func TestQUICHostPoolLeavesTransportParameterOrderUnseeded(t *testing.T) {
	preset := deterministicQUICPreset(t)
	pool := newSeedTestQUICPool(t, preset, 4242)

	if _, seed := quicConnClientHello(t, pool); seed != 0 {
		t.Fatalf("QUIC connection carries transport-parameter shuffle seed %d, want 0 (reshuffle per serialization)", seed)
	}
}
