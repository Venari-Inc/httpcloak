---
title: SOCKS5 UDP
sidebar_position: 5
---

# SOCKS5 UDP

SOCKS5 has a `UDP ASSOCIATE` command (RFC 1928 section 4) that lets the client send and receive UDP datagrams through the proxy. httpcloak uses this to push HTTP/3 (QUIC over UDP) through a SOCKS5 server.

The flow is two-part:

1. A TCP control connection to the SOCKS5 server, doing the normal greeting + auth, then sending a UDP ASSOCIATE request.
2. The proxy replies with a relay address (a UDP host:port). The client opens a local UDP socket and sends every datagram to that relay address, wrapped in a small SOCKS5 UDP header carrying the real target. Replies come back the same way.

QUIC packets get wrapped, sent through the relay, unwrapped on the proxy, forwarded to the target, and replies wrapped on the way back. To QUIC it just looks like a regular UDP socket.

## Wiring it up

You need both a TCP proxy (for the H1/H2 path) and a UDP proxy (for the H3 path) on the same SOCKS5 endpoint. Set both:

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
package main

import (
    "context"
    "fmt"

    "github.com/sardanioss/httpcloak"
)

func main() {
    proxyURL := "socks5://user:pass@proxy.example.com:1080"

    s := httpcloak.NewSession("chrome-latest",
        httpcloak.WithSessionTCPProxy(proxyURL),
        httpcloak.WithSessionUDPProxy(proxyURL),
        httpcloak.WithForceHTTP3(),
    )
    defer s.Close()

    resp, err := s.Get(context.Background(), "https://httpbin.org/ip")
    if err != nil {
        panic(err)
    }
    body, _ := resp.Text()
    fmt.Println(resp.StatusCode, body)
}
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

proxy = "socks5://user:pass@proxy.example.com:1080"

with httpcloak.Session(
    preset="chrome-latest",
    tcp_proxy=proxy,
    udp_proxy=proxy,
    http_version="h3",
) as s:
    r = s.get("https://httpbin.org/ip")
    print(r.status_code, r.text)
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const { Session } = require('httpcloak');

const proxy = 'socks5://user:pass@proxy.example.com:1080';

const s = new Session({
  preset: 'chrome-latest',
  tcpProxy: proxy,
  udpProxy: proxy,
  httpVersion: 'h3',
});

const r = await s.get('https://httpbin.org/ip');
console.log(r.statusCode, r.body);
s.close();
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

const string proxy = "socks5://user:pass@proxy.example.com:1080";

using var s = new Session(
    preset: "chrome-latest",
    tcpProxy: proxy,
    udpProxy: proxy,
    httpVersion: "h3");

var r = await s.GetAsync("https://httpbin.org/ip");
Console.WriteLine($"{r.StatusCode} {r.Body}");
```

</TabItem>
</Tabs>

Mixing also works: HTTP CONNECT for TCP, SOCKS5 UDP ASSOCIATE for the UDP slot. That's a legal combo as long as both proxies exist and reach the same target.

:::warning UDP ASSOCIATE is not universal
Not every SOCKS5 server supports `UDP_ASSOCIATE`. The RFC says servers MAY support it, not MUST. Plenty of residential providers don't, and on a load-balanced endpoint you might land on a server that doesn't even when others on the same hostname do.

A server that doesn't speak UDP replies with `reply=7` (Command not supported) or `reply=1` (general SOCKS server failure). httpcloak retries up to 5 times on `reply=1` because that's the typical load-balancer-hits-a-bad-server symptom. After that it gives up.

Test before trusting it for H3 routing. Simplest test: configure `udp_proxy`, force H3 against `https://httpbin.org/ip`. If it errors with "UDP ASSOCIATE failed" you don't have UDP support.
:::

## What the SOCKS5 UDP header looks like

Every datagram sent to the relay is prefixed with:

```
+----+------+------+----------+----------+----------+
|RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
+----+------+------+----------+----------+----------+
| 2  |  1   |  1   | Variable |    2     | Variable |
+----+------+------+----------+----------+----------+
```

`FRAG` is always 0. httpcloak doesn't fragment, and it refuses to read fragmented datagrams from the proxy because reassembly is messy and never happens in practice. The address is the *target* host:port. ATYP follows the same enum as the TCP path: 1 IPv4, 3 domain, 4 IPv6.

## Keepalive on the control channel

Per RFC 1928, when the TCP control connection closes, the UDP relay goes away with it. httpcloak enables TCP keepalive on the control connection (15s period) and runs a goroutine that reads from the control socket so dropped connections get noticed quickly.

For long-lived QUIC connections this matters. If you start an H3 request and the SOCKS5 control channel dies 30 seconds in, your QUIC connection silently stops getting packets through. That's the kind of hang you'd debug for an hour before realizing the control socket dropped.

## Common errors

- `UDP ASSOCIATE failed: command not supported`: server doesn't speak UDP relay. Pick a different provider or a different server.
- `UDP ASSOCIATE failed: general SOCKS server failure`: usually means you hit a load-balanced backend that doesn't do UDP. httpcloak retries up to 5 times before giving up.
- `fragmented packets not supported (frag=N)`: a proxy is sending fragmented datagrams, which is rare and usually a misconfiguration.
- QUIC handshake timeout after UDP ASSOCIATE succeeds: the relay exists but isn't forwarding packets. Some proxies advertise UDP and then silently drop everything. Annoying but real. Try a different endpoint.

## When MASQUE is the better answer

If your provider also offers a MASQUE endpoint, that's usually a smoother way to tunnel H3. SOCKS5 UDP works but the ecosystem is patchy and the per-datagram header overhead is real. See [MASQUE](./masque) for the H3-native alternative.
