---
title: Overview
sidebar_position: 2
---

# Overview

Four proxy protocols are supported. The right one depends on what your upstream offers and the kind of traffic you're routing through it.

## What we support

| Type | URL scheme | Carries | Auth | Best for |
|---|---|---|---|---|
| HTTP CONNECT | `http://`, `https://` | TCP (HTTP/1.1, HTTP/2) | Basic | The classic. Datacenter proxies, corporate egress, anything fronting as a normal HTTP proxy. |
| SOCKS5 | `socks5://`, `socks5h://` | TCP (HTTP/1.1, HTTP/2) | none, user/pass | Residential providers default to this. BrightData, Smartproxy, Oxylabs, SOAX. |
| SOCKS5 with UDP ASSOCIATE | `socks5://` (UDP slot) | UDP (HTTP/3 over QUIC) | none, user/pass | Routing H3 through a SOCKS5 server that supports UDP relay. Less common, not every provider does it. |
| MASQUE (CONNECT-UDP) | `masque://`, `https://` | UDP inside HTTP/3 | Basic | The new kid. Tunnels QUIC inside QUIC. A handful of residential providers (Bright Data, Oxylabs, Smartproxy, SOAX) auto-detect from `https://` URLs; any other provider needs the explicit `masque://` scheme. |

The short version: HTTP CONNECT covers HTTP/1.1 and HTTP/2, SOCKS5 covers the same when your residential provider hands you a SOCKS5 endpoint, and a UDP proxy (SOCKS5 UDP or MASQUE) only enters the picture when you want HTTP/3 to ride through the proxy too.

## Split-config: TCP and UDP separately

The TCP proxy and the UDP proxy can be configured independently with two options on the same session:

- `WithSessionTCPProxy(url)` sends HTTP/1.1 and HTTP/2 through this proxy.
- `WithSessionUDPProxy(url)` sends HTTP/3 (QUIC) through this proxy.

Splitting them helps when your TCP proxy can't carry UDP. Common shapes:

```go
// HTTP CONNECT for H1/H2 + MASQUE for H3
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithSessionTCPProxy("http://user:pass@proxy.example.com:8080"),
    httpcloak.WithSessionUDPProxy("masque://user:pass@proxy.example.com:443"),
)

// SOCKS5 for everything (only works if SOCKS5 server supports UDP ASSOCIATE)
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithSessionTCPProxy("socks5://user:pass@proxy.example.com:1080"),
    httpcloak.WithSessionUDPProxy("socks5://user:pass@proxy.example.com:1080"),
)

// HTTP CONNECT only, kill H3 because no UDP route
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithSessionTCPProxy("http://user:pass@proxy.example.com:8080"),
    httpcloak.WithDisableHTTP3(),
)
```

If only `WithSessionTCPProxy` is set and the UDP slot is left empty, H3 dials direct. That's fine on most networks. On a network that blocks outbound UDP/443, pair it with `WithDisableHTTP3()` so the session sticks to H1/H2.

The older `WithSessionProxy(url)` option still works and applies the URL to both slots based on scheme. New code should prefer the split form, it's clearer about what's going where.

## Auth

Auth lives in the URL. All three forms work:

- `http://user:pass@host:port`
- `socks5://user:pass@host:port`
- `masque://user:pass@host:port`

For HTTP CONNECT and MASQUE, the credentials become a `Proxy-Authorization: Basic <base64>` header. For SOCKS5 they go through RFC 1929 username/password sub-negotiation. URL-encode the password if it has special characters, that's standard URL parsing.

## Source-address binding

Independent of any proxy choice, every dial can be pinned to a specific local IP. `WithLocalAddress("203.0.113.10")` or `WithLocalAddrIP(net.IP)` binds every outgoing socket (direct or through a proxy) to that address. On Linux it sets `IP_FREEBIND` so addresses from a routed prefix that aren't on any interface still work without `CAP_NET_ADMIN`.

See [Source Address Binding](./source-address-binding) for the full shape, IPv6 prefix rotation tricks, and platform notes.

## Picking the right one

A rough decision tree:

- Datacenter or corporate proxy gave you a host:port and HTTP basic auth: HTTP CONNECT.
- Residential provider gave you a SOCKS5 endpoint: [SOCKS5](./socks5).
- Same residential provider, you want H3 to also go through the proxy and they advertise UDP support: [SOCKS5 UDP](./socks5-udp).
- You want H3 traffic tunneled through a residential-proxy provider that ships MASQUE, or through a custom MASQUE server: [MASQUE](./masque).
- You want a single source IP no matter the proxy choice: [Source Address Binding](./source-address-binding).

## Bindings

The same options exist in Python (`tcp_proxy=`, `udp_proxy=`, `local_address=`), Node.js (`tcpProxy`, `udpProxy`, `localAddress`), and .NET (`tcpProxy:`, `udpProxy:`, `localAddress:`). The chapters that follow show 4-language tabs wherever the API differs.
