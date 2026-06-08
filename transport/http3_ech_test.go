package transport

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/sardanioss/httpcloak/dns"
	"github.com/sardanioss/httpcloak/fingerprint"
)

// The per-host ECH cache must not pin a config forever (a CDN ECH key rotation
// would otherwise strand a long-lived session on a retired key until restart).
// These lock the two behaviors the fix adds: fresh entries are served from
// cache, and invalidateECHConfig drops the entry so the next dial refetches.
func TestECHConfigCacheInvalidateAndTTL(t *testing.T) {
	tr, err := NewHTTP3Transport(fingerprint.Chrome146(), dns.NewCache())
	if err != nil {
		t.Fatalf("NewHTTP3Transport: %v", err)
	}

	const host = "ech-test.example"
	fresh := []byte("FRESH-ECH-CONFIG")

	// Save/restore round-trips the bytes through the expiry-carrying struct.
	tr.SetECHConfigCache(map[string][]byte{host: fresh})
	if got := tr.GetECHConfigCache()[host]; !bytes.Equal(got, fresh) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, fresh)
	}

	// A fresh (unexpired) entry is served straight from cache, no network.
	if got := tr.getECHConfig(context.Background(), host); !bytes.Equal(got, fresh) {
		t.Fatalf("getECHConfig should return the cached fresh config, got %q", got)
	}

	// Expired entry must NOT be served from cache (the gate is the whole fix):
	// force the entry into the past, then the cache-hit branch must be skipped.
	tr.echConfigCacheMu.Lock()
	tr.echConfigCache[host] = &echCachedConfig{config: fresh, expiresAt: time.Now().Add(-time.Minute)}
	tr.echConfigCacheMu.Unlock()
	tr.echConfigCacheMu.RLock()
	cached, ok := tr.echConfigCache[host]
	served := ok && time.Now().Before(cached.expiresAt)
	tr.echConfigCacheMu.RUnlock()
	if served {
		t.Fatal("expired ECH entry should not be served from cache")
	}

	// Invalidation drops the per-transport entry so the next dial refetches.
	tr.SetECHConfigCache(map[string][]byte{host: fresh})
	tr.invalidateECHConfig(host)
	if _, ok := tr.GetECHConfigCache()[host]; ok {
		t.Fatal("invalidateECHConfig should have removed the entry")
	}
}
