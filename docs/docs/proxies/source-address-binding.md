---
title: Source Address Binding
sidebar_position: 7
---

# Source Address Binding

Source address binding pins every outgoing socket to a specific local IP. The reasons it comes up:

- Your machine has multiple public IPs and you want to pick one.
- A routed IPv6 prefix (`/64` or wider) is available and you want to rotate source IPs per request out of a huge pool.
- Audit / compliance requires a known egress IP.
- You're testing IPv6 behavior on a dual-stack box.

httpcloak gives you two options for this. They both set the same internal field, pick whichever ergonomic fits your code.

## The options

`WithLocalAddress(string)` takes a string IP. Both v4 and v6 work:

```go
httpcloak.WithLocalAddress("192.168.1.100")
httpcloak.WithLocalAddress("2001:db8::dead:beef")
```

`WithLocalAddrIP(net.IP)` takes a parsed `net.IP`. Useful when you've got an IP from a pool or a randomizer and you don't want to round-trip through a string. Nil is a no-op so you can safely write `opts = append(opts, httpcloak.WithLocalAddrIP(maybeNil))` without clobbering a previously-set address.

```go
ip := pickRandomFromPool() // returns net.IP
httpcloak.WithLocalAddrIP(ip)
```

A related option, `WithSessionPreferIPv4()`, isn't about local binding but commonly comes up next to it. It flips the DNS cache's `PreferIPv4` flag, which biases address ordering so the IPv4 record sorts ahead of the AAAA record. Happy Eyeballs in the H3 dial path still races whatever addresses come back, but with v4 first the v4 leg gets the head start. Use it on networks where IPv6 is half-broken; the upshot in practice is that v4 wins the dial unless v6 is significantly faster.

## What it does at the socket layer

Every outgoing socket (direct dial, dial-to-proxy, UDP for QUIC) gets `LocalAddr` set to the chosen IP with port 0 (kernel picks the ephemeral port). The kernel then tries to bind that source address before the connect.

On Linux, httpcloak also calls `setsockopt(IP_FREEBIND)` and `setsockopt(IPV6_FREEBIND)` on the raw socket before the bind. Why this matters next.

## Linux `IP_FREEBIND`: bind addresses you don't "own"

By default a Linux box won't let you bind to an IP that isn't configured on any of its interfaces. You'd get `EADDRNOTAVAIL`. `IP_FREEBIND` (and the IPv6 equivalent) skip that check. The kernel trusts that you know what you're doing.

This is what makes IPv6 prefix rotation cheap. Your hoster routes a `/64` (or `/56`, or `/48`) to your box. You don't have to configure 18 quintillion addresses on the interface. You just bind to whichever address you want from the prefix and `IP_FREEBIND` lets the bind succeed. Outgoing packets carry that source address, return packets get routed back because the upstream router knows the prefix is yours. Pretty slick.

httpcloak applies `FREEBIND` unconditionally on Linux. Failures get silently ignored. If the kernel rejects it, the bind would have worked anyway because the address was locally configured, and we don't want to fail the simple case.

:::tip IPv6 /64 rotation
If you have a `/64` (or wider) routed to your box and want a fresh source IP per request, generate a random suffix and pass it to `WithLocalAddrIP`. Linux freebind handles the rest, no `ip addr add` required.
:::

## Permissions on Linux

`IP_FREEBIND` works without root in two cases:

- Per-process: granted via `CAP_NET_ADMIN` (rare for userland).
- System-wide: `sysctl net.ipv4.ip_nonlocal_bind=1` and the IPv6 equivalent `net.ipv6.ip_nonlocal_bind=1`.

Most production setups go with the sysctl. Drop this in `/etc/sysctl.d/`:

```
net.ipv4.ip_nonlocal_bind = 1
net.ipv6.ip_nonlocal_bind = 1
```

Without one of these, binding to a non-configured address still fails even with `FREEBIND` set. The setsockopt is necessary but not sufficient on stock kernels.

## Platform notes

- **Linux**: Full support. `IP_FREEBIND` + sysctl as above.
- **macOS / Darwin**: Bind works for addresses configured on an interface. Non-local bind isn't a thing the way Linux does it.
- **Windows**: Same as macOS.
- **Other Unix**: `freebind_other.go` is a no-op. The bind goes through but non-local addresses will fail at the kernel.

If you run on Linux and your code is portable, design for the Linux behavior and accept the other platforms as best-effort.

## Examples

### Pin to a specific IPv4

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithLocalAddress("203.0.113.10"),
)
```

</TabItem>
<TabItem value="python" label="Python">

```python
s = httpcloak.Session(
    preset="chrome-latest",
    local_address="203.0.113.10",
)
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const s = new Session({
  preset: 'chrome-latest',
  localAddress: '203.0.113.10',
});
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using var s = new Session(
    preset: "chrome-latest",
    localAddress: "203.0.113.10");
```

</TabItem>
</Tabs>

### Bind to a specific IPv6

```go
import "net"

s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithLocalAddrIP(net.ParseIP("2001:db8::1")),
)
```

### Rotating IPv6 from a /64

```go
import (
    "crypto/rand"
    "net"
)

func randomFromPrefix64(prefix net.IP) net.IP {
    suffix := make([]byte, 8)
    _, _ = rand.Read(suffix)
    out := make(net.IP, 16)
    copy(out[:8], prefix.To16()[:8])
    copy(out[8:], suffix)
    return out
}

prefix := net.ParseIP("2001:db8:abcd:1234::") // your routed /64
ip := randomFromPrefix64(prefix)

s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithLocalAddrIP(ip),
)
defer s.Close()

resp, _ := s.Get(ctx, "https://httpbin.org/ip")
// returns the random v6 address you picked
```

Run that for each fresh request (a new session per IP, or pool the sessions by IP). The session itself caches one local address for its lifetime. Rotation happens at session-construction time.

### Combined with a proxy

Local binding and proxy options compose. The local address binds the *client to proxy* socket; the proxy still picks its own egress for the target connection.

```go
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithSessionTCPProxy("socks5://user:pass@proxy.example.com:1080"),
    httpcloak.WithLocalAddress("203.0.113.10"),
)
```

This pattern fits when your machine has multiple egress IPs and you want the SOCKS5 control connection to leave from a known one (routing, firewall ACLs, etc).
