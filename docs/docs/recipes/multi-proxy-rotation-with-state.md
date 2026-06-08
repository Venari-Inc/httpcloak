---
title: Multi-Proxy Rotation With State
sidebar_position: 1
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Multi-Proxy Rotation With State

Proxy rotation is swapping the upstream IP between requests so the target sees a different client each time. The naive way is to build a fresh client per proxy, which throws away every bit of TLS state along with the IP. The pattern in this recipe keeps one `Session` and only swaps the proxy underneath, so the fingerprint, tickets, and cookies all carry across rotations.

## Why session continuity matters

A rotator that builds a new client per swap pays for a fresh TCP plus TLS handshake on every request, and the server sees a brand new visitor every time. That works on soft targets that don't track returning visitors. On anything with session tracking layered on top, looking like a first-time visitor for 500 requests in a row is a dead giveaway.

Here is what gets lost between two fresh handshakes:

- **TLS extension order** drifts because GREASE rotates per connection. The preset and browser version are the same, but the bytes on the wire shift.
- **Session tickets** are gone. The next handshake is full instead of resumed, so timing and key-exchange shape are different.
- **ECH state** resets. If the target uses ECH, the config gets refetched from scratch.
- **Cookie jar** resets unless you copy it across by hand.
- **Per-connection tracking** like Cloudflare's `__cf_bm` cookie ages oddly when the same cookie hops hosts and IPs.

Keeping one `Session` and swapping only the proxy underneath preserves all of that. The IP is the only thing that changes, and the IP is the only thing the rotation was for.

:::tip
Most residential proxy providers don't track session continuity themselves, so the pattern is invisible from their side. The benefit shows up on the target, where session tracking is the layer that flags brand-new visitors.
:::

## The pattern

1. Build one `Session` with your preset (e.g. `chrome-latest`).
2. For each request:
   - Pick a proxy from the pool.
   - Call `session.SetTCPProxy(url)` (plus `SetUDPProxy` if H3 is in play).
   - Send the request.
`SetTCPProxy` rebuilds the underlying transports, so the next request dials fresh through the new proxy. There's no extra `Refresh()` to call between rotations.

The session is the unit of state. Proxies are configuration that change underneath it.

:::info `SetTCPProxy` already drops connections
`SetTCPProxy` (and `SetProxy` / `SetUDPProxy`) close the H1, H2, and H3 transports and rebuild them with the new proxy config. An extra `Refresh()` between proxy swaps is a no-op at best; at worst it discards TLS session tickets that would otherwise resume on the next handshake. The example below leaves it out.
:::

## Full example: rotating through 3 proxies

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/sardanioss/httpcloak"
)

// In production, load this from a file or your provider's API.
// We use placeholder URLs here so the example doesn't ship credentials.
var proxyPool = []string{
    "http://user1:pass1@proxy1.example.com:8080",
    "http://user2:pass2@proxy2.example.com:8080",
    "http://user3:pass3@proxy3.example.com:8080",
}

func main() {
    // ONE session for the whole run. Proxy is set per-request below.
    s := httpcloak.NewSession("chrome-latest",
        httpcloak.WithSessionTimeout(30*time.Second),
    )
    defer s.Close()

    targets := []string{
        "https://tls.peet.ws/api/all",
        "https://tls.peet.ws/api/all",
        "https://tls.peet.ws/api/all",
        "https://tls.peet.ws/api/all",
    }

    for i, url := range targets {
        // Round-robin pick. Swap for random / weighted / sticky-by-host
        // depending on what your target wants.
        proxy := proxyPool[i%len(proxyPool)]
        s.SetTCPProxy(proxy)

        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        resp, err := s.Get(ctx, url)
        cancel()
        if err != nil {
            fmt.Printf("[req %d] proxy=%s err=%v\n", i, proxy, err)
            continue
        }
        body, _ := resp.Text()
        resp.Close()
        fmt.Printf("[req %d] proxy=%s status=%d body_len=%d\n",
            i, proxy, resp.StatusCode, len(body))
    }
}
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

PROXY_POOL = [
    "http://user1:pass1@proxy1.example.com:8080",
    "http://user2:pass2@proxy2.example.com:8080",
    "http://user3:pass3@proxy3.example.com:8080",
]

with httpcloak.Session("chrome-latest", timeout=30) as s:
    for i in range(4):
        proxy = PROXY_POOL[i % len(PROXY_POOL)]
        s.set_tcp_proxy(proxy)

        try:
            r = s.get("https://tls.peet.ws/api/all")
            print(f"[req {i}] proxy={proxy} status={r.status_code}")
        except Exception as e:
            print(f"[req {i}] proxy={proxy} err={e}")
```

Full Python API lives at [/bindings/python](../bindings/python).

</TabItem>
</Tabs>

## What survives a rotation

After `SetTCPProxy(newProxy)`:

| State | Survives? | Notes |
|-------|-----------|-------|
| Cookie jar | Yes | Lives on the session, not on the transport |
| ECH config (DNS-side) | Yes | Cached per host in `dns.echCache`, process-wide |
| Header order, preset, DNS cache | Yes | Session-level, not per-connection |
| Custom JA3, H2 settings, TCP fingerprint | Yes | Re-applied to the rebuilt transports |
| TLS session tickets | Depends | Default in-memory cache is per-transport, so lost on rebuild. A pluggable shared cache via `WithSessionCache` survives the swap |
| HTTP/1, HTTP/2, HTTP/3 connections | No | Closed and rebuilt with the new proxy config |
| TCP / UDP sockets | No | Reopen through the new proxy on the next request |

The point of the swap is that the live sockets drop. Cookies and the bigger session-level state stay attached, and any per-host TLS resumption that you want preserved across rotations needs a shared `SessionCacheBackend`.

## Rotation strategies

### Round-robin

```go
proxy := proxyPool[i%len(proxyPool)]
```

Cheap, predictable, works for most cases.

### Sticky-by-host

When the scrape hits multiple hosts and you want one proxy per host, use a map keyed by hostname:

```go
hostProxy := map[string]string{}

for _, url := range urls {
    host := parseHost(url)
    if _, ok := hostProxy[host]; !ok {
        hostProxy[host] = pickFromPool()
    }
    s.SetTCPProxy(hostProxy[host])
    // ... send
}
```

Useful when servers correlate the IP a session started on with later requests on that session. Starting a Cloudflare challenge on IP A and finishing it on IP B is the kind of inconsistency that gets flagged.

### Rotate-on-error

Stay on one proxy until something fails, then swap. Pool consumption stays low, and proxies only get cycled when there is a reason to cycle them.

```go
err := doRequest(s)
if err != nil || isBadStatus(resp.StatusCode) {
    s.SetTCPProxy(nextProxy())
}
```

## H3 / QUIC notes

HTTP/3 traffic goes over UDP, which standard HTTP and SOCKS5 proxies do not handle. To rotate H3 through a proxy, the upstream needs to be MASQUE, configured as the UDP proxy:

```go
s.SetTCPProxy("http://user:pass@http-proxy:8080")
s.SetUDPProxy("masque://user:pass@masque-proxy:443")
```

Most commercial proxy providers only sell TCP exits. Setting `SetTCPProxy` without `SetUDPProxy` leaves H3 to dial UDP directly, which leaks the real client IP on any request that races H3 successfully. Either wire both transports through proxies or force H1/H2 with `WithForceHTTP2()`.

## Combining with Save / LoadSession

For runs that span hours and need to survive a process restart:

```go
// Periodically:
s.Save("/var/lib/scraper/state.json")

// On startup:
s, _ := httpcloak.LoadSession("/var/lib/scraper/state.json")
s.SetTCPProxy(currentProxy)
```

`Save` writes the cookie jar, ticket cache, and ECH state to disk. `LoadSession` reads them back. The pattern in full is in [Long-Running Scraper Patterns](./long-running-scraper-patterns).

## Common mistakes

**Building a new session per proxy.** This is the failure mode the recipe exists to fix. A new session means new TLS state, new cookies, and a brand-new visitor on every rotation. One session, many proxies.

**Calling `Refresh()` after `SetTCPProxy`.** Don't. `SetTCPProxy` (via `Transport.SetProxy`) already closes the H1, H2, and H3 transports and rebuilds them with the new proxy config, so the next request dials fresh through the new proxy on its own. An extra `Refresh()` just throws away TLS session tickets that would otherwise resume on the next handshake.

**Mixing UDP and TCP proxies.** H3 dials UDP and uses `SetUDPProxy`; H1 and H2 dial TCP and use `SetTCPProxy`. Setting one and leaving the other unset means protocol racing happily picks the unproxied side and bypasses your proxy entirely.

## Related

- [Refresh](../connection-lifecycle/refresh), what `Refresh()` does
- [Proxies overview](../proxies/overview), supported proxy types
- [SOCKS5](../proxies/socks5), SOCKS5 specifics
- [MASQUE](../proxies/masque), UDP / H3 proxying
