---
title: Speculative TLS
sidebar_position: 3
---

# Speculative TLS

A standard HTTP CONNECT proxy dial costs two round-trips before any TLS bytes hit the wire:

1. Client opens TCP to the proxy.
2. Client writes `CONNECT target:443 HTTP/1.1` and waits.
3. Proxy writes `HTTP/1.1 200 Connection established` and waits.
4. Client writes the TLS ClientHello.
5. Proxy relays it. Server replies. Handshake continues.

Steps 2-3 burn one full RTT. Steps 4 onward are the actual TLS handshake. On a 50ms-RTT proxy, that's 50ms of dead air doing nothing useful, every time a fresh proxied dial goes out.

`WithEnableSpeculativeTLS` collapses that round-trip. The CONNECT request and the inner ClientHello get written to the socket in the same burst, before the proxy has a chance to reply with its 200. A well-behaved proxy reads the CONNECT, sets up the upstream tunnel, and immediately starts forwarding the bytes that came after the `\r\n\r\n`. The 200 still comes back, but the ClientHello overlaps with it instead of waiting for it. One round-trip saved.

:::tip
Free win for any proxy-heavy workload. If you're making lots of fresh proxied dials, flip this on.
:::

## What it looks like on the wire

A non-speculative dial sends two distinct write bursts to the proxy:

```
burst 1: CONNECT httpbin.org:443 HTTP/1.1\r\nHost: httpbin.org:443\r\n\r\n
[wait for 200]
burst 2: \x16\x03\x01... (TLS ClientHello)
```

A speculative dial coalesces them:

```
burst 1: CONNECT httpbin.org:443 HTTP/1.1\r\nHost: httpbin.org:443\r\n\r\n\x16\x03\x01... (CONNECT + ClientHello in the same write)
[200 comes back overlapping with the upstream forwarding]
```

The lib still parses the proxy's 200 response correctly, the proxy still sees a valid CONNECT request. The only thing that changes is the timing on the wire.

## Turning it on

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
    s := httpcloak.NewSession("chrome-latest",
        httpcloak.WithSessionTCPProxy("http://user:pass@proxy.example.com:8080"),
        httpcloak.WithEnableSpeculativeTLS(),
    )
    defer s.Close()

    resp, err := s.Get(context.Background(), "https://httpbin.org/ip")
    if err != nil {
        panic(err)
    }
    fmt.Println(resp.StatusCode)
}
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

with httpcloak.Session(
    preset="chrome-latest",
    tcp_proxy="http://user:pass@proxy.example.com:8080",
    enable_speculative_tls=True,
) as s:
    r = s.get("https://httpbin.org/ip")
    print(r.status_code)
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const { Session } = require('httpcloak');

const s = new Session({
  preset: 'chrome-latest',
  tcpProxy: 'http://user:pass@proxy.example.com:8080',
  enableSpeculativeTls: true,
});

const r = await s.get('https://httpbin.org/ip');
console.log(r.statusCode);
s.close();
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(
    preset: "chrome-latest",
    tcpProxy: "http://user:pass@proxy.example.com:8080",
    enableSpeculativeTls: true);

var r = await s.GetAsync("https://httpbin.org/ip");
Console.WriteLine(r.StatusCode);
```

</TabItem>
</Tabs>

## When it doesn't help

- **No proxy.** Speculative TLS is a CONNECT-path optimization. Direct dials don't have a CONNECT exchange to fold the ClientHello into, so the option is a no-op.
- **SOCKS5 proxies.** SOCKS has its own framing and the lib doesn't apply the speculative trick on the SOCKS path. Stick with HTTP CONNECT for this win.
- **Already-warm connections.** The savings only land on the first dial. Once the H2 or H1 connection is in the pool, requests reuse it and there's no proxy handshake left to optimize.

## When it can break

Some proxies are picky. They expect to read the CONNECT, write the 200, and only then start reading more bytes. If the client sends extra bytes before the 200 is fully written, the proxy might do one of three things:

- Buffer the speculative ClientHello correctly and forward it upstream once the tunnel's up. Common case.
- Reject the CONNECT outright with a parse error. Rare, but seen on older Squid setups and a handful of debugging tools.
- Drop the speculative bytes silently, leaving the inner TLS handshake stuck waiting for the server's reply. Worst case.

If a proxy looks like it's misbehaving, turn the option off and re-test. A fresh dial that works without speculative and hangs with it is the signal. Write the proxy brand down somewhere so future you remembers.

## Compatibility status by proxy class

- Modern commercial residential and datacenter proxies: works. Squid 4+, Tinyproxy, mitmproxy in upstream mode, Bright Data, Oxylabs, the usual suspects. Verified in production.
- Squid 3.x with old defaults: hit or miss. Test before you trust.
- Corporate egress proxies (BlueCoat, Zscaler, Forcepoint): mostly untested in this project. Some inspect the CONNECT carefully and may not tolerate extra bytes before the 200.
- TLS-terminating MITM proxies: doesn't matter. They terminate inside the proxy and re-originate, so the ClientHello sent by the client isn't the one that reaches the target anyway.

## Pairing with other features

Speculative TLS composes with the rest of the lib:

- Works on H1 and H2 dial paths (HTTP CONNECT through an HTTP or SOCKS5 proxy). H3 / MASQUE is a separate dial path that doesn't go through `SpeculativeConn` today, so the optimisation doesn't apply to QUIC traffic.
- Works with `WithECHFrom`. The ECH-wrapped ClientHello is what gets pipelined.
- Works with custom JA3 / JA4 fingerprints.
- Works with session resumption. The speculative ClientHello can carry a PSK and resume in one round-trip.

Stack speculative TLS, session resumption, and ECH on a proxy-heavy H1/H2 workload and the first request reaches data in one round-trip with the SNI encrypted on the way out. That's a Chrome-class profile very few clients ship with by default.

## When the optimisation backfires

A small fraction of CONNECT proxies are strict about the byte order on the wire and reject a CONNECT request that arrives with extra application bytes pipelined behind it. Speculative TLS sends the CONNECT and the ClientHello back-to-back, so the proxy sees both in one read; a strict parser reads the CONNECT, replies, then chokes on the ClientHello bytes still in the buffer. The lib catches the failure and surfaces it as `transport.SpeculativeTLSError`:

```go
import "github.com/sardanioss/httpcloak/transport"

resp, err := s.Get(ctx, url)
if err != nil {
    if transport.IsSpeculativeTLSError(err) {
        // proxy rejected pipelining; retrying without speculative TLS
    }
}
```

There's a manual escape valve too: if the application has already learned that a particular proxy can't take pipelined bytes, mark it once and the lib stops trying speculative TLS for that proxy address for the rest of the process lifetime:

```go
transport.MarkProxyNoSpeculative("proxy.example.com:443")

// optionally check before configuring a session
if transport.IsProxyNoSpeculative("proxy.example.com:443") {
    // skip WithEnableSpeculativeTLS for this proxy
}
```

The mark is a process-global set keyed by proxy address. It survives across sessions but doesn't persist across restarts; rebuild it on startup if your proxy pool has known-bad entries.
