package dns

import (
	"testing"
	"time"
)

// InvalidateECHConfig must drop the cached entry so the next FetchECHConfigs
// re-queries DNS instead of serving a stale config after a CDN key rotation.
func TestInvalidateECHConfig(t *testing.T) {
	const host = "ech-invalidate-test.example"
	echCacheMu.Lock()
	echCache[host] = &ECHEntry{ConfigList: []byte("STALE"), ExpiresAt: time.Now().Add(time.Hour)}
	echCacheMu.Unlock()

	InvalidateECHConfig(host)

	echCacheMu.RLock()
	_, ok := echCache[host]
	echCacheMu.RUnlock()
	if ok {
		t.Fatal("InvalidateECHConfig should have removed the cached entry")
	}
}
