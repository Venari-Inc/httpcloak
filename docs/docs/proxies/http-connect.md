---
title: HTTP CONNECT
sidebar_position: 3
---

# HTTP CONNECT

HTTP CONNECT is the original HTTP proxy method. The client opens TCP to the proxy, sends `CONNECT host:port HTTP/1.1`, the proxy comes back with `200 Connection established`, and from there the socket is just a raw tunnel. The real TLS handshake then runs through that tunnel like the proxy isn't even there.

This is what HTTPS-through-an-HTTP-proxy looks like on the wire. Squid, mitmproxy in upstream mode, every datacenter proxy provider, almost every corporate egress proxy: all HTTP CONNECT.

## URL shape

```
http://user:pass@proxy.example.com:8080
https://user:pass@proxy.example.com:8443
```

`https://` is for cases where the connection from your client to the proxy itself should be TLS. It's not super common, but some providers offer it. The inner request is always TLS regardless, since that's the target site's TLS, decrypted only at the target.

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
        httpcloak.WithSessionTCPProxy("http://user:pass@proxy.example.com:8080"),
    )
    defer s.Close()

    resp, err := s.Get(context.Background(), "https://httpbin.org/ip")
    if err != nil {
        panic(err)
    }
    body, _ := resp.Text()
    fmt.Println(resp.StatusCode, body)
    // {"origin": "<the proxy's egress IP>"}
}
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

with httpcloak.Session(
    preset="chrome-latest",
    tcp_proxy="http://user:pass@proxy.example.com:8080",
) as s:
    r = s.get("https://httpbin.org/ip")
    print(r.status_code, r.text)
    # {"origin": "<the proxy's egress IP>"}
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const { Session } = require('httpcloak');

const s = new Session({
  preset: 'chrome-latest',
  tcpProxy: 'http://user:pass@proxy.example.com:8080',
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
    tcpProxy: "http://user:pass@proxy.example.com:8080");

var r = await s.GetAsync("https://httpbin.org/ip");
Console.WriteLine($"{r.StatusCode} {r.Body}");
```

</TabItem>
</Tabs>

## What's on the wire

A normal request through an HTTP CONNECT proxy goes:

1. TCP open to `proxy.example.com:8080`.
2. Client sends `CONNECT httpbin.org:443 HTTP/1.1` plus `Proxy-Authorization: Basic <base64(user:pass)>` and a host header.
3. Proxy replies `HTTP/1.1 200 Connection established`.
4. Client sends the TLS ClientHello on the same socket. From here the proxy is just forwarding bytes.
5. TLS handshake completes against `httpbin.org`. Client sends `GET /ip`.

That's two round-trips before TLS even starts (TCP handshake, then CONNECT exchange), then one or two more for TLS depending on resumption.

## Saving an RTT with speculative TLS

httpcloak can pipeline the CONNECT request with the inner ClientHello. Same socket, both writes coalesced before any read. That saves one full round-trip on every fresh proxied connection.

```go
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithSessionTCPProxy("http://user:pass@proxy.example.com:8080"),
    httpcloak.WithEnableSpeculativeTLS(),
)
```

It's off by default because some proxies (older Squid configs, a handful of debugging tools) freak out when bytes show up before the 200 is fully read. Test it on your provider first. Full details and trade-offs in [Speculative TLS](/advanced-tls/speculative-tls).

## H3 with an HTTP CONNECT TCP proxy

HTTP CONNECT only carries TCP. Setting just `WithSessionTCPProxy` with an HTTP URL leaves H3 dialing direct to the target with no proxy on the UDP side. Three options:

- Let H3 dial direct (the default). Fine on most networks.
- Add a UDP proxy: `WithSessionUDPProxy("masque://...")` or a SOCKS5 endpoint that supports UDP ASSOCIATE.
- Disable H3: `WithDisableHTTP3()` so the session sticks to H1/H2 and everything goes through the HTTP CONNECT proxy.

## Auth

Basic auth is handled for you when the credentials live in the URL. httpcloak base64-encodes `user:pass` and adds `Proxy-Authorization: Basic ...` to the CONNECT request. If the password has special characters, URL-encode them:

```
http://user:p%40ss%21@proxy.example.com:8080
```

(That's `p@ss!`.)

## Common errors

- `407 Proxy Authentication Required`: credentials missing or wrong. Check the URL has `user:pass@`.
- `403 Forbidden`: proxy reached you but rejected the target. Some providers block specific destinations. Try a different target to confirm.
- `502 Bad Gateway`: proxy couldn't reach the target. Often the proxy's upstream having a bad day.
- `failed to resolve proxy host ...`: your DNS can't resolve the proxy hostname itself. Most often a typo.

## Provider quirks

Some HTTP CONNECT providers are picky about the order or presence of headers in the CONNECT request. httpcloak sends a minimal CONNECT (method line, Host header, optional Proxy-Authorization). If a provider needs a specific User-Agent or extra header on the CONNECT line itself, that's not currently configurable. File an issue with the provider name if you hit this.
