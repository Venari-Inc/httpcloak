---
title: Insecure Skip Verify
sidebar_position: 6
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';


# Insecure Skip Verify

`WithInsecureSkipVerify()` flips the certificate verification check off. The session still does a full TLS handshake, the server still presents its certificate chain, the lib still parses what it gets back. The verification step (chain build, signature check, hostname match, expiry) is the part that gets skipped.

The TLS fingerprint is unchanged. uTLS still picks the preset's cipher list, extension order, supported groups, and ALPN entries. SNI still goes out under the target hostname. Only the verdict at the end of the handshake changes from "reject if invalid" to "accept anything that completes".

## When to use it

- Self-signed certs in dev / staging environments that you control. The chain wouldn't validate against any public root, and you don't want to install a private CA on the dev box.
- Routing through an interception proxy (Burp Suite, mitmproxy, Charles) that re-signs traffic with its own CA. Importing the proxy CA into the system trust store also works, this flag is the lower-friction path for one-off captures.
- Hitting an internal service whose CA isn't in the system trust roots and that you can't change.
- Never in production. The flag accepts any cert from any peer, including a hostile one mid-path. Ship it on, and your TLS layer offers no peer authentication.

## How it propagates internally

The option flips `c.insecureSkipVerify = true` on the session config. When the transport builds its `tls.Config`, that bit becomes `tls.Config.InsecureSkipVerify = true`, which uTLS reads during handshake. uTLS still receives the server's `Certificate` message, still parses the chain into `[]*x509.Certificate`, still hands it to `VerifyConnection` callbacks if you registered one. What it doesn't do is run `Certificate.Verify` against the system root pool. That's the single check that gets skipped.

The chain is still available to the application after the handshake. If you want to do your own per-request inspection (custom CA pin, fingerprint match, expiry warning) the cleanest path is the cert pinning surface in the [Cert Pinning](./cert-pinning) chapter. The pin runs after the handshake on the same chain and rejects the connection if your pin doesn't match. Combining `WithInsecureSkipVerify()` with a `client.Client.PinCertificate(hash, ...)` call lets you bypass system-CA verification while still authenticating the peer against a known certificate or public-key hash.

## Code

The bindings expose this through a `verify` boolean (default `true`). Setting it to `false` is the same thing as Go's `WithInsecureSkipVerify()` under the hood.

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
        httpcloak.WithInsecureSkipVerify(),
    )
    defer s.Close()

    resp, err := s.Get(context.Background(), "https://self-signed.example/")
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

with httpcloak.Session(preset="chrome-latest", verify=False) as s:
    r = s.get("https://self-signed.example/")
    print(r.status_code)
```

</TabItem>
<TabItem value="node" label="Node.js">

```js
const { Session } = require('httpcloak');

const s = new Session({ preset: 'chrome-latest', verify: false });
const r = await s.get('https://self-signed.example/');
console.log(r.statusCode);
s.close();
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(preset: "chrome-latest", verify: false);

var r = s.Get("https://self-signed.example/");
Console.WriteLine(r.StatusCode);
```

</TabItem>
</Tabs>

## What it does NOT do

- It does not change the TLS fingerprint. JA3 / JA4 / Akamai hashes are identical with the flag on or off.
- It does not disable SNI. The target hostname still appears in the ClientHello (or the encrypted inner ClientHello when ECH is in play).
- It does not partially loosen verification. The check is binary: with the flag, every certificate is accepted, including ones with a wrong hostname, an expired notAfter, or a chain that doesn't link to anything trusted.
- It does not change H2 / H3 negotiation. ALPN still picks the highest the server offers, and the preset's protocol preference still applies.
- It does not pin around the cert pinner. If both `WithInsecureSkipVerify` and a registered cert pin (via `client.Client.PinCertificate`) are set, pinning still runs after the handshake completes, since pinning is checked at the application layer, not as part of cert verification.

## Verifying it's actually skipping verify

The cleanest probe is a self-signed certificate. Spin up a one-shot Go server with `crypto/tls` generating an in-memory cert, point a session with `WithInsecureSkipVerify()` at it, then point a session without the flag at the same address. The first request returns a 200, the second fails with an `x509: certificate signed by unknown authority` error before any HTTP request goes out.

A quicker check using public infrastructure: `https://self-signed.badssl.com/`. Without the flag, the session errors on the verification step. With the flag, the request completes and you get the badssl page body back.

For the MITM-proxy case, set `https_proxy=http://127.0.0.1:8080` for Burp / mitmproxy in the environment, then run a request through the session. Without the flag, you get an `x509: certificate signed by unknown authority` error referencing the proxy's CA. With the flag, the request flows through and you can see the decrypted traffic in the proxy's UI.

## Scope and lifetime

The flag is per-session. Two `Session` instances in the same process can have different verification policies. A scraper hitting public targets keeps verification on, a debug session pointed at the dev cluster has the flag set, and they coexist without interfering with each other's TLS contexts. Each session builds its own `tls.Config`, so there's no shared global state to worry about.

The flag stays in effect across `Refresh()`. Refreshing a session rebuilds the underlying transport but reuses the session config, so verification stays off after refresh. If you want verification back on, build a new `Session` without the option.

The flag does not survive serialization through `LocalProxy` registry. When you register a session via `RegisterSession(id, *Session)`, the session keeps its own config including this flag, so per-tenant verification policies work as expected.

## Common pitfall: verification off, hostname wrong

A session with `WithInsecureSkipVerify()` set will happily complete a handshake against a server presenting a certificate for a totally different hostname. That's by design (everything is accepted), but it leads to confusing failures later. If the target service does Server Name Indication routing on its side and the cert says "wrong-host.example", the request might land on the wrong virtual host even though the TCP connection succeeded. The lib won't flag this, since the verification step is exactly what would catch it. Watch your application-layer responses for surprises.

The fix in those cases is cert pinning (authenticates the peer without using the system trust store; see the [Cert Pinning](./cert-pinning) chapter) or, for hostname mismatches that are cosmetic only (an internal service with a cert issued for an IP), letting your reverse proxy terminate TLS so the cert match becomes a non-issue.

A second pitfall is forgetting to remove the flag before deploying. CI environments sometimes inherit dev configs, and the resulting binary in production talks TLS without authenticating its peer. Treat this option like a `DEBUG=true` switch: easy to flip on locally, requires a second pair of eyes on the diff before it ships.
