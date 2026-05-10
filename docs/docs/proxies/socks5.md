---
title: SOCKS5
sidebar_position: 4
---

# SOCKS5

SOCKS5 is the residential-proxy workhorse. The client connects to the SOCKS5 server, runs a short version + auth handshake, asks the server to CONNECT to the target host, and from there the socket is a TCP tunnel. httpcloak runs its real TLS handshake through that tunnel.

It's the SOCKS5 cousin of HTTP CONNECT, with a binary handshake instead of a text one and an auth scheme that handles username/password as a proper sub-protocol.

:::info
Most residential providers (BrightData, Smartproxy, Oxylabs, SOAX, IPRoyal, etc) ship SOCKS5 endpoints by default. If you bought "rotating residential proxies" from someone in the last few years, what they handed you was almost certainly SOCKS5.
:::

## URL shapes

```
socks5://proxy.example.com:1080
socks5://user:pass@proxy.example.com:1080
socks5h://proxy.example.com:1080
```

`socks5` and `socks5h` both work. httpcloak always sends hostname targets to the proxy as a SOCKS5 domain ATYP (address type 3), which means DNS resolution of the *target* happens at the proxy. The *proxy's own hostname* is always resolved client-side, that part you can't avoid.

If your URL has an IP literal as the target host (rare, you'd build that yourself), httpcloak sends an IPv4 or IPv6 ATYP instead.

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
        httpcloak.WithSessionTCPProxy("socks5://user:pass@proxy.example.com:1080"),
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
    tcp_proxy="socks5://user:pass@proxy.example.com:1080",
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
  tcpProxy: 'socks5://user:pass@proxy.example.com:1080',
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
    tcpProxy: "socks5://user:pass@proxy.example.com:1080");

var r = await s.GetAsync("https://httpbin.org/ip");
Console.WriteLine($"{r.StatusCode} {r.Body}");
```

</TabItem>
</Tabs>

## Auth schemes

httpcloak negotiates the auth method based on what's in the URL.

- No `user:pass` in the URL: client offers `0x00` (NO AUTHENTICATION REQUIRED) only. If the proxy demands auth it'll fail the handshake.
- `user:pass` in the URL: client offers both no-auth and `0x02` (USERNAME/PASSWORD, RFC 1929). Server picks one. If it picks username/password, httpcloak runs the sub-negotiation.

An authenticated URL works fine against an open proxy too, the server just picks no-auth and the credentials sit unused. URL-encode special characters in the password the same way you'd do it for HTTP CONNECT:

```
socks5://user:p%40ss%21@proxy.example.com:1080
```

GSSAPI (auth method `0x01`) isn't supported. If your provider needs it, raise an issue.

## SOCKS5 vs SOCKS5h

`socks5h://` is a curl-ism that means "delegate DNS to the proxy". httpcloak treats both schemes identically because it always sends hostname targets as a SOCKS5 domain ATYP, which is exactly the "DNS-at-proxy" behavior. The scheme suffix is accepted but doesn't change anything you'd notice on the wire.

To force client-side DNS for the target, resolve the hostname yourself before building the URL. Usually not what you want with residential providers, since the proxy's DNS view is part of why you're using it.

## H3 through SOCKS5

A vanilla `WithSessionTCPProxy("socks5://...")` only routes TCP. H3 (QUIC over UDP) will dial direct to the target. To send H3 through the SOCKS5 server too, the server needs to support UDP ASSOCIATE and a UDP proxy needs to be wired up. See [SOCKS5 UDP](./socks5-udp).

## Common errors

- `SOCKS5 handshake failed: failed to read auth response`: usually the proxy closed the socket. Check the URL host/port and credentials.
- `proxy rejected all authentication methods`: server didn't accept no-auth or user/pass. You probably need to add credentials.
- `authentication failed: invalid credentials`: self-explanatory, typo or bad creds.
- `CONNECT failed: connection refused (reply=5)`: target host or port refused the connection. Try a different target.
- `CONNECT failed: host unreachable (reply=4)`: target DNS resolved at the proxy to something unroutable, or the proxy can't reach it.

The `reply=N` codes follow RFC 1928 section 6, useful when grepping provider docs.

## Source-IP binding combined with SOCKS5

`WithLocalAddress` works with SOCKS5 too. The local address binds the socket from your machine to the SOCKS5 server. The proxy still picks its own egress IP for the upstream side, you don't get to influence that from the client. Use this when your machine has multiple public IPs and you want to route the SOCKS5 control connection out a specific one.
