---
title: DNS Cache
sidebar_position: 8
---

# DNS Cache

`dns.Cache` is the in-memory resolver every transport sits on top of. It exists for two reasons: a single host gets dialed many times in a session and re-resolving every time wastes round trips, and Happy Eyeballs needs both A and AAAA records sorted in a specific order. The cache handles both, and exposes the same surface to user code that the lib uses internally.

The cache is created automatically inside `NewSession`. You don't usually need to touch it. When you do (custom Happy Eyeballs ordering, pre-warming, statistics, manual invalidation), the surface is small and lives in the `dns` subpackage.

## Getting hold of the cache

A live session's cache is reachable via the transport:

```go
import "github.com/sardanioss/httpcloak"

s := httpcloak.NewSession("chrome-latest")
defer s.Close()

cache := s.GetTransport().GetDNSCache()
```

For a fresh standalone cache (running outside a session, or seeding a custom transport):

```go
import "github.com/sardanioss/httpcloak/dns"

c := dns.NewCache()
```

Defaults: 5-minute TTL, 30-second floor (caps any user-supplied TTL below 30s), CGO resolver (the pure-Go resolver doesn't work reliably when the binary is loaded as a shared library).

## Resolution methods

```go
func (c *Cache) Resolve(ctx, host) ([]net.IP, error)
func (c *Cache) ResolveOne(ctx, host) (net.IP, error)
func (c *Cache) ResolveAllSorted(ctx, host) ([]net.IP, error)
func (c *Cache) ResolveIPv6First(ctx, host) (ipv6, ipv4 []net.IP, err error)
```

`Resolve` returns every IP in the order the system resolver hands them back. `ResolveOne` picks one address with `PreferIPv4` honoured (default behaviour is IPv6-first to match modern browser dialing). `ResolveAllSorted` interleaves IPv6 and IPv4 in the RFC 8305 Happy Eyeballs order, again flippable by `SetPreferIPv4(true)`. `ResolveIPv6First` returns the two families separately so a custom dialer can race them in a specific order.

If a hostname is already an IP literal, every method short-circuits and returns it directly without touching the resolver.

## TTL and expiry

```go
c.SetTTL(2 * time.Minute)
```

`SetTTL` sets the cache TTL applied to fresh resolutions. Anything below the 30-second minimum gets clamped up. The TTL is the lib's, not the upstream DNS TTL; the resolver returns the IPs without the TTL field, so the lib applies its own. A short TTL hammers DNS more, a long one risks serving stale records.

A stale-but-cached entry is also a fallback: if a fresh lookup fails (network blip, resolver timeout) and the host has a previously cached entry, the cache returns the stale entry instead of failing. The stale entry stays in place until the next successful resolution overwrites it.

## Invalidation and cleanup

```go
c.Invalidate("example.com")
c.Clear()
```

`Invalidate` drops one host. `Clear` empties the whole cache. Both are O(1) and O(n) respectively, and lock the cache while running.

For long-running processes, the cache also exposes a janitor:

```go
c.Cleanup()                                       // one-shot expired sweep
c.StartCleanup(ctx, 5*time.Minute)                // background sweeper
```

`Cleanup` walks the map once and removes anything past `ExpiresAt`. `StartCleanup` spawns a goroutine that runs `Cleanup` on the given interval until `ctx` is cancelled. The transport doesn't run a cleanup goroutine by default; entries stay in the map until they get overwritten by the next resolution. For a process that resolves thousands of hosts and never calls them again, kick off a `StartCleanup` to bound memory.

## Statistics

```go
total, expired := c.Stats()
```

`total` is every entry currently in the cache (fresh or stale). `expired` is the subset past TTL. The difference is what's actively serving cache hits. Useful for an observability endpoint or a periodic log line.

## Pre-warming

There's no dedicated `Prewarm` helper, but a `Resolve` call against a host populates the cache the same way a request would. Run a one-shot loop at startup if your hot path can't afford the first-request DNS latency:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

cache := s.GetTransport().GetDNSCache()
for _, h := range []string{"a.example.com", "b.example.com"} {
    _, _ = cache.Resolve(ctx, h)
}
```

## ECH DNS server overrides

ECH discovery (HTTPS RR lookup) is a separate code path with its own cache and its own resolver. By default the lib queries `8.8.8.8:53`, `1.1.1.1:53`, and `9.9.9.9:53` in that order on UDP, with a 500ms per-server timeout (ECH is treated as best-effort, so a slow resolver doesn't stall the dial). To redirect those queries to a specific resolver (for example a corporate DNS that DOES return HTTPS RR, or a local DoH proxy):

```go
import "github.com/sardanioss/httpcloak/dns"

dns.SetECHDNSServers([]string{"10.0.0.53:53"})
servers := dns.GetECHDNSServers()
dns.SetECHDNSServers(nil) // reset to the default 8.8.8.8 / 1.1.1.1 / 9.9.9.9 trio
```

Both functions are package-level and process-wide, not per-cache. They affect every ECH lookup the binary makes from the moment they're set. The 500ms timeout is hard-coded; an ECH lookup that doesn't come back in 500ms is treated as "no ECH available" and the dial proceeds without it. See [ECH](../advanced-tls/ech) for the higher-level ECH workflow.

## Bindings

The Python (`httpcloak.set_ech_dns_servers([...])`) and Node (`setEchDnsServers([...])`) bindings expose the override directly. The .NET binding ships a managed wrapper at `HttpCloak.HttpCloakInfo.SetEchDnsServers(string[])` / `GetEchDnsServers()`.

Per-cache controls (TTL, manual `Resolve` / `Invalidate`, stats) are Go-only. The bindings drive the cache implicitly through `Session` and don't surface the `Cache` struct directly. If a binding caller needs explicit cache control, the workaround is a Go-side helper exposed through `LocalProxy` or the bindings' raw cgo entry points.
