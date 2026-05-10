---
title: Session Manager
sidebar_position: 7
---

# Session Manager

`session.Manager` is an in-process registry for many sessions at once. One Go binary running a worker pool, a multi-tenant scraper that needs N persistent identities, or a service that exposes "give me a session by ID" semantics over an internal API. The Manager handles ID generation, idle eviction, and clean shutdown without you having to maintain a `map[string]*Session` and a janitor goroutine yourself.

The top-level package re-exports it. `httpcloak.Manager` is a type alias for `session.Manager`, and `httpcloak.NewManager()` is the constructor. The same value lives at `session.Manager` for callers who already import the subpackage:

```go
import "github.com/sardanioss/httpcloak"

m := httpcloak.NewManager()
defer m.Shutdown()
```

## When to reach for it

You don't need it for a single client process talking to a single target; the regular `httpcloak.NewSession` is fine for that. The Manager is the right tool when:

- A long-running service holds multiple sessions and needs to look them up by external ID (worker pool, multi-tenant proxy, API server).
- You want bounded session counts with automatic eviction of idle ones.
- You want one place to call `Shutdown()` that closes everything cleanly.

The `LocalProxy` chapter ([Local Proxy Server](/recipes/local-proxy-server)) uses a similar registry but tied to the proxy's session-selection header. The Manager is the more general primitive.

## Construction and defaults

```go
m := httpcloak.NewManager()
```

The defaults are:

| Setting | Default | Override |
|---|---|---|
| Max concurrent sessions | 100 | `m.SetMaxSessions(n)` |
| Idle timeout | 30 min | `m.SetSessionTimeout(d)` |
| Cleanup interval | 1 min | (not exposed; runs in background goroutine) |

A background goroutine wakes every minute, walks the session map, and closes anything that has been idle past the timeout. The cleanup is opportunistic; an in-flight request resets `LastUsed`, so an active session won't get reaped underneath you.

## API surface

```go
func (m *Manager) CreateSession(config *protocol.SessionConfig) (string, error)
func (m *Manager) GetSession(sessionID string) (*Session, error)
func (m *Manager) CloseSession(sessionID string) error
func (m *Manager) ListSessions() []SessionStats
func (m *Manager) SessionCount() int
func (m *Manager) Shutdown()
func (m *Manager) SetMaxSessions(max int)
func (m *Manager) SetSessionTimeout(timeout time.Duration)
```

`CreateSession` generates a unique ID and returns it. `GetSession` looks up by ID and errors if the session is missing or already closed. `CloseSession` closes one. `Shutdown` closes the lot, including the cleanup goroutine, and is what you call from a signal handler. `ListSessions` returns a snapshot of stats for every active session.

## A worker-pool sketch

```go
package main

import (
    "context"
    "log"
    "sync"
    "time"

    "github.com/sardanioss/httpcloak"
    "github.com/sardanioss/httpcloak/protocol"
)

func main() {
    m := httpcloak.NewManager()
    m.SetMaxSessions(50)
    m.SetSessionTimeout(15 * time.Minute)
    defer m.Shutdown()

    // Build N sessions up front, one per worker identity.
    ids := make([]string, 0, 10)
    for i := 0; i < 10; i++ {
        cfg := &protocol.SessionConfig{Preset: "chrome-latest"}
        id, err := m.CreateSession(cfg)
        if err != nil {
            log.Fatalf("create: %v", err)
        }
        ids = append(ids, id)
    }

    // Hand each worker a session by ID.
    var wg sync.WaitGroup
    for _, id := range ids {
        wg.Add(1)
        go func(sessionID string) {
            defer wg.Done()
            s, err := m.GetSession(sessionID)
            if err != nil {
                log.Printf("[%s] get: %v", sessionID, err)
                return
            }
            ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
            defer cancel()
            // ... drive s.Get / s.Do as normal ...
            _ = ctx
            _ = s
        }(id)
    }
    wg.Wait()
}
```

`SessionConfig` is the same struct that the bindings serialize over the cgo boundary; every field that `httpcloak.NewSession` accepts as an option lives in there. See `protocol/types.go` in the source tree for the full field list.

## Eviction and explicit close

A session reaped by the idle timer is closed cleanly: TCP and QUIC connections are torn down, cookie state is dropped, the entry leaves the map. A subsequent `GetSession(id)` returns `session not found`. If you persist session state (`Save` / `Marshal`), call it before the idle window passes; the Manager doesn't snapshot state for you.

Explicit `CloseSession(id)` is the right tool when a session has been compromised (for example a proxy banned the IP) and you want a fresh identity to take its place. `CreateSession` afterwards yields a fresh unique ID.

## Limits

`SetMaxSessions(n)` is a hard cap. `CreateSession` returns `maximum sessions limit reached` once `SessionCount()` reaches it; reap an old session or raise the cap before retrying. The default of 100 is conservative; production deployments often run higher.

The Manager doesn't queue; there's no "wait for a slot to open up". Backpressure for over-saturated worker pools belongs in the application layer, in front of the Manager.

## Shutdown behaviour

`Shutdown()` is one-shot. It closes the cleanup goroutine via the internal shutdown channel, then walks the session map and closes every entry. Calling `Shutdown()` twice will panic on the closed channel; guard with `sync.Once` if your shutdown path can fire twice.

```go
var shutdownOnce sync.Once
defer shutdownOnce.Do(m.Shutdown)
```

## Bindings

Python and Node ship a higher-level "session by ID" pattern through `LocalProxy.RegisterSession` (the proxy maps an inbound `X-HTTPCloak-Session` header to the right session). The .NET binding has the same pattern via `LocalProxy.RegisterSession`. None of the bindings expose `session.Manager` directly today; if you're driving N sessions from a non-Go process, the proxy-based pattern in [Local Proxy Server](/recipes/local-proxy-server) is the supported path.
