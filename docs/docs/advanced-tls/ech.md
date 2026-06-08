---
title: ECH
sidebar_position: 2
---

# ECH

ECH stands for Encrypted Client Hello. In a regular TLS 1.3 handshake the SNI field sits in plaintext on the wire, so anyone in the network path (ISP, corporate middlebox, nation-state observer) can read which hostname the connection is reaching even though the rest of the handshake is encrypted. ECH wraps the real ClientHello inside a second handshake aimed at an ECH provider's outer name, leaving an observer with something generic like `cloudflare-ech.com` and the inner target hidden behind it.

httpcloak ships with ECH on by default. When the target host publishes an ECH config in its DNS HTTPS RR, the lib fetches it, attaches the ECH extension to the ClientHello, and the inner SNI ends up encrypted. When the host doesn't publish one, the connection falls back to a plain ClientHello and the request goes through normally. The absence of ECH is never a hard failure.

:::info
ECH is still rolling out. Plenty of sites haven't published HTTPS RR records yet, so the fallback kicks in often. No failure when the record's missing, just a normal SNI on the wire.
:::

## How httpcloak picks the ECH config

The selection order is short:

1. If `WithECHFrom(domain)` is set, fetch the HTTPS RR for that alternate domain and use its ECH config.
2. Otherwise, fetch the HTTPS RR for the target host and use whatever it publishes.
3. If neither path returns a config, send a normal ClientHello with plaintext SNI.

For H3, this lookup runs in parallel with the A/AAAA DNS resolution, so it adds close to zero latency on the first connection. For H2, the ECH lookup only runs when you opt in via `WithECHFrom`. (See the H2 caveat below.)

## Default session, ECH on

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
    s := httpcloak.NewSession("chrome-latest")
    defer s.Close()

    resp, err := s.Get(context.Background(), "https://example.com/")
    if err != nil {
        panic(err)
    }
    fmt.Println(resp.StatusCode, resp.Protocol)
}
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

with httpcloak.Session(preset="chrome-latest") as s:
    r = s.get("https://example.com/")
    print(r.status_code, r.protocol)
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const { Session } = require('httpcloak');

const s = new Session({ preset: 'chrome-latest' });
const r = await s.get('https://example.com/');
console.log(r.statusCode, r.protocol);
s.close();
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(preset: "chrome-latest");
var r = await s.GetAsync("https://example.com/");
Console.WriteLine($"{r.StatusCode} {r.Protocol}");
```

</TabItem>
</Tabs>

That's the whole opt-in. No flag needed. If `example.com` published an ECH config (it doesn't, today), the SNI on the wire would be encrypted. Since it doesn't, you get a plain handshake and the request still works.

## Turning ECH off

`WithDisableECH` skips the DNS HTTPS RR lookup entirely. The session won't include the ECH extension in the ClientHello, won't run the parallel HTTPS RR fetch, won't probe at all. Useful for shaving a few ms off the first connection in a known-no-ECH environment, or for debugging when a plain SNI on the wire makes the capture easier to read.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithDisableECH(),
)
```

</TabItem>
</Tabs>

The Python, Node, and .NET bindings don't currently expose a kwarg for disabling ECH. The lookup runs by default and there's no per-session opt-out from those languages. If you need it disabled from a binding, the workaround is a Go-side `LocalProxy` constructed with `WithDisableECH()` and the binding's HTTP client pointed at the proxy.

There's no security hit from turning ECH off. The only thing lost is the SNI privacy bit. Everything else about the handshake is identical.

## Borrowing an ECH config from another domain

Some hosts don't publish their own HTTPS RR but sit behind a CDN that does. Cloudflare runs a public ECH endpoint at `cloudflare-ech.com`, and any Cloudflare-proxied origin can be reached using that ECH config because the outer handshake terminates at Cloudflare's edge regardless of which inner hostname is targeted.

`WithECHFrom(domain)` tells the lib to fetch the HTTPS RR from `domain` instead of from the target host. The fetched config is used for any request on that session.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithECHFrom("cloudflare-ech.com"),
)
defer s.Close()

resp, _ := s.Get(context.Background(), "https://example.com/")
fmt.Println(resp.StatusCode)
```

</TabItem>
<TabItem value="python" label="Python">

```python
with httpcloak.Session(
    preset="chrome-latest",
    ech_config_domain="cloudflare-ech.com",
) as s:
    r = s.get("https://example.com/")
    print(r.status_code)
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const s = new Session({
  preset: 'chrome-latest',
  echConfigDomain: 'cloudflare-ech.com',
});
const r = await s.get('https://example.com/');
console.log(r.statusCode);
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using var s = new Session(
    preset: "chrome-latest",
    echConfigDomain: "cloudflare-ech.com");

var r = await s.GetAsync("https://example.com/");
Console.WriteLine(r.StatusCode);
```

</TabItem>
</Tabs>

## Verifying ECH actually fired

This is the annoying part. There's no public reflector that prints "yes, you used ECH". The options are:

- Capture the connection in Wireshark with the [keylog trick](./tls-keylog) and look for the `encrypted_client_hello` extension (type `0xfe0d`) in the ClientHello. The outer SNI will be the ECH provider's name, not the target.
- Inspect the DNS lookup directly. The `dns` package exposes `FetchECHConfigsBase64(ctx, host)` for direct import. Non-empty base64 means the host publishes ECH and the lib will use it.
- Trust the fallback. If `WithECHFrom` is set and the target is Cloudflare-fronted, ECH almost certainly fired.

Quick sanity probe in Go:

```go
import hcdns "github.com/sardanioss/httpcloak/dns"

b64, _ := hcdns.FetchECHConfigsBase64(ctx, "cf.erika.cool")
fmt.Println("ECH config len:", len(b64))
```

Empty means the host doesn't publish (or the DNS path is broken). Non-empty means the lib has a real config to plug into the ClientHello.

## Caveats

- **ECH on H1/H2 is opt-in only.** The H3 dial path auto-fetches the target's HTTPS RR. The H2 path only consults `WithECHFrom` (session-level) and `client.WithECHConfig` (the alternate `client.Client` API in the `httpcloak/client` subpackage, which takes a raw config blob), it doesn't auto-probe the target. The H1 path doesn't touch ECH at all. For sessions forced to H1/H2, set `WithECHFrom` explicitly. (There's a bug filed against this in the project's internal tracker.)
- ECH requires TLS 1.3. The lib bumps `MinVersion` to 1.3 automatically when an ECH config is present.
- The HTTPS RR lookup is cached per host for the configured TTL, so repeated requests don't re-resolve.
- ECH config rotation: providers rotate keys every few hours. httpcloak caches the HTTPS RR for the TTL the DNS server returned, then refetches on the next dial after expiry. There's no error-driven invalidation today; a stale-but-not-yet-expired config will keep getting served until the TTL window passes. Force a refresh by waiting out the TTL (most resolvers return ~300s for HTTPS records) or by recreating the session, which builds a fresh transport with an empty per-transport ECH cache.
