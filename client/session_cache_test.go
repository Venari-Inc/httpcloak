package client

import (
	"testing"

	utls "github.com/sardanioss/utls"
)

// Regression lock for the Venari WithSessionCache patch.
//
// The fork exists partly to carry client.WithSessionCache, which hands a shared
// TLS session cache to the pool manager so a PSK primed on a residential proxy
// is still resumed after the fetch switches to a datacenter proxy. ByteKit's
// only consumer is packages/http-fetcher/internal/session/session.go.
//
// The failure this locks against is silent: upstream reworked client.Client
// heavily in the v1.6.8 -> v1.6.11 window, and a rebase can leave the option
// compiling and setting ClientConfig.SessionCache while the value never reaches
// the manager. Nothing would error; sessions would simply stop resuming and
// every proxy switch would pay a full handshake.
//
// Deliberately a wiring assertion rather than a live handshake: it must stay
// green offline and in CI, where the network fingerprint echo hosts are not
// reachable.
func TestWithSessionCache_ThreadsCacheIntoManager(t *testing.T) {
	cache := utls.NewLRUClientSessionCache(64)

	c := NewClient("chrome-151", WithSessionCache(cache))
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	defer c.Close()

	if c.poolManager == nil {
		t.Fatal("client has no pool manager; the cache cannot have been threaded anywhere")
	}

	got := c.poolManager.GetSessionCache()
	if got == nil {
		t.Fatal("pool manager has no session cache: WithSessionCache did not reach the manager")
	}
	if got != cache {
		t.Fatalf("pool manager holds a different cache than the one passed: got %p, want %p", got, cache)
	}
}

// Companion arm: absent the option, nothing invents a cache. This is what makes
// the assertion above meaningful rather than trivially true.
func TestWithoutSessionCache_ManagerHasNoCache(t *testing.T) {
	c := NewClient("chrome-151")
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	defer c.Close()

	if c.poolManager == nil {
		t.Fatal("client has no pool manager")
	}

	if got := c.poolManager.GetSessionCache(); got != nil {
		t.Fatalf("pool manager has a session cache with no WithSessionCache option: %p", got)
	}
}
