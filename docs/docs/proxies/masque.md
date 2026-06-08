---
title: MASQUE
sidebar_position: 6
---

# MASQUE

MASQUE is HTTP/3's answer to tunneling UDP through a proxy. The client opens an HTTP/3 connection to the proxy, sends an Extended CONNECT request with `:protocol = connect-udp` against the well-known path, and on success uses HTTP/3 Datagrams (RFC 9297) to push UDP payload bytes back and forth.

For httpcloak, the inner UDP payload is QUIC packets aimed at the real target. So you get QUIC inside QUIC. The outer QUIC encrypts your tunnel to the proxy, the inner QUIC encrypts your traffic to the target.

The relevant RFCs:

- RFC 9298: CONNECT-UDP (the method, the well-known path, the framing)
- RFC 9297: HTTP/3 Datagrams (the carrier for inner UDP)
- RFC 9484: the MASQUE WG output document tying it together

## URL shapes

```
masque://proxy.example.com:443
masque://user:pass@proxy.example.com:443
https://user:pass@proxy.example.com:443    # auto-detected for known providers
```

`masque://` is just a scheme hint. Internally it gets normalized to `https://` because the proxy connection is plain HTTPS-over-H3. If the hostname matches a known MASQUE provider (BrightData, Oxylabs, Smartproxy, SOAX), `https://` is auto-detected as MASQUE. For any other provider use `masque://` explicitly so httpcloak doesn't try to treat the URL as a regular HTTPS proxy.

Default port is 443. The proxy must speak HTTP/3 with Extended CONNECT and HTTP/3 Datagrams enabled.

## Setting it up

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
        httpcloak.WithSessionUDPProxy("masque://user:pass@proxy.example.com:443"),
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

with httpcloak.Session(
    preset="chrome-latest",
    udp_proxy="masque://user:pass@proxy.example.com:443",
    http_version="h3",
) as s:
    r = s.get("https://httpbin.org/ip")
    print(r.status_code, r.text)
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const { Session } = require('httpcloak');

const s = new Session({
  preset: 'chrome-latest',
  udpProxy: 'masque://user:pass@proxy.example.com:443',
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

using var s = new Session(
    preset: "chrome-latest",
    udpProxy: "masque://user:pass@proxy.example.com:443",
    httpVersion: "h3");

var r = await s.GetAsync("https://httpbin.org/ip");
Console.WriteLine($"{r.StatusCode} {r.Body}");
```

</TabItem>
</Tabs>

To send H1/H2 through a proxy as well, pair `udp_proxy` with `tcp_proxy`. The common shape: HTTP CONNECT for TCP, MASQUE for UDP.

## What's on the wire

1. UDP socket open, QUIC handshake to `proxy.example.com:443`. ALPN is `h3`, datagram support negotiated, Extended CONNECT confirmed via `SETTINGS_ENABLE_CONNECT_PROTOCOL`.
2. Client opens an HTTP/3 request stream and sends:
   ```
   :method = CONNECT
   :protocol = connect-udp
   :scheme = https
   :authority = proxy.example.com:443
   :path = /.well-known/masque/udp/<target_host>/<target_port>/
   capsule-protocol = ?1
   proxy-authorization = Basic <base64>     # if creds present
   ```
3. Proxy replies with a 2xx status. The request stream stays open.
4. Every QUIC packet for the real target gets wrapped in an HTTP/3 Datagram with context ID 0 (per RFC 9298), sent on the same QUIC connection. Replies come back the same way.

The inner QUIC connection runs an entirely separate handshake against the actual target host. The proxy can't read it.

## Provider auto-detection

httpcloak ships a small list of known MASQUE-capable providers in [proxy/masque_providers.go](https://github.com/sardanioss/httpcloak/blob/main/proxy/masque_providers.go). If your `https://` URL matches one of those hostnames it gets handled as MASQUE automatically. For any other provider, use `masque://` explicitly. That's the unambiguous form.

The list can be extended at runtime:

```go
import "github.com/sardanioss/httpcloak/proxy"

proxy.AddMASQUEProvider("my-custom-masque.example.com")
```

After that, `https://my-custom-masque.example.com:443` will be treated as MASQUE. Process-wide, applied immediately to subsequent sessions.

## QUIC config knobs that matter

- `EnableDatagrams` is forced on for the proxy connection. Required.
- `InitialPacketSize` defaults to 1350 to leave room for the outer QUIC frame plus inner QUIC's ~1200 MTU. Don't set it lower.
- The browser preset's QUIC fingerprint applies to the outer connection (the one to the proxy). The inner connection to the target also uses the preset, so the target sees a normal browser QUIC handshake.

## Common errors

- `proxy doesn't support Extended CONNECT`: the proxy isn't a MASQUE server. You're hitting a regular HTTPS endpoint or an H3 server that hasn't enabled Extended CONNECT. Check the URL.
- `proxy doesn't support HTTP/3 Datagrams`: same family, datagrams weren't negotiated. Provider misconfig or just not a MASQUE endpoint.
- `proxy responded with 407`: auth failed. Check `user:pass` in the URL.
- `proxy responded with 403`: target blocked by the proxy.
- `proxy responded with 502/503`: proxy can't reach the target over UDP, or the target's QUIC ALPN handshake failed.

## When to pick MASQUE over SOCKS5 UDP

MASQUE was designed from scratch for tunneling UDP. The framing is clean, congestion control is the outer QUIC's problem, and providers that ship it usually take it seriously. SOCKS5 UDP_ASSOCIATE works but the ecosystem support is patchy and the framing is per-datagram.

If your provider offers both, prefer MASQUE for H3.
