---
title: Certificate Pinning
sidebar_position: 6
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Certificate Pinning

Certificate pinning enforces an exact-match check on the peer's cert (or, more usefully, its public key) at the TLS layer. Even when a CA somewhere in the chain gets compromised, a corporate proxy injects its own root, or an inspection box silently MITMs every request, the handshake fails because the key on the wire isn't the one nailed down in the client.

That last case is what matters for red team work. Plenty of "transparent" inspection boxes on internal networks own a trusted root, so a standard cert chain validates fine and the request leaks straight into the inspector. Pin the SPKI and the inspector's substituted cert no longer matches.

## Pin types

Two flavors:

| Type | What it hashes | When to use |
|---|---|---|
| `PinTypeSHA256` | SHA256 of the cert's Subject Public Key Info (SPKI) | Default. Survives cert renewals as long as the keypair stays the same. |
| `PinTypeCertificate` | SHA256 of the full DER cert | Stricter. Breaks the second the cert renews, even with the same key. |

SPKI hashing is what HPKP and Chrome's static pin list use. Stick with SPKI unless there's a specific reason not to.

There's also a file-based path: load a PEM cert off disk and httpcloak extracts the SPKI from it. Same pin type under the hood, less copy-pasting hashes around.

## Client-level pinning

The fastest path. The `*client.Client` exposes pin methods directly:

```go
import "github.com/sardanioss/httpcloak/client"

c := client.NewClient("chrome-latest")

// Pin by base64 SPKI hash
c.PinCertificate("YSxNUV05SLc2H4Z6kOXWCsUPPMenylyBVtogFlUiByE=", client.ForHost("example.com"))

// Or load it from a PEM file
_ = c.PinCertificateFromFile("/etc/ssl/example.com.crt", client.ForHost("example.com"))

// Drop everything
c.ClearPins()

// Get the underlying pinner if you want raw control
pinner := c.CertPinner()
```

`PinCertificate` is the one to reach for 90% of the time. Pass the base64 SPKI hash, optionally scope it with `ForHost(...)` and `IncludeSubdomains()`, done.

`PinCertificateFromFile` parses a PEM cert and extracts the SPKI on the way in. Useful when the cert is already sitting in a file and there's no need to pipe it through openssl first.

`ClearPins` wipes every pin on the client. `CertPinner` returns the underlying pinner for direct calls to `AddPin`, `GetPins`, `HasPins`.

## Standalone CertPinner

A pinner can also be built outside any client:

```go
p := client.NewCertPinner()

p.AddPin("YSxNUV05SLc2H4Z6kOXWCsUPPMenylyBVtogFlUiByE=",
    client.ForHost("example.com"),
    client.IncludeSubdomains(),
)

_ = p.AddPinFromCertFile("/etc/ssl/backup.crt", client.ForHost("example.com"))

_ = p.AddPinFromPEM(pemBytes, client.ForHost("api.example.com"))

// Verify yourself, given a chain
err := p.Verify("example.com", peerCerts)
```

Client-attached vs standalone: use Client-attached when httpcloak is doing the request and pinning should be enforced automatically on every response. Use standalone when chains come from somewhere else (a stored cert dump, a different transport, a custom dial) and the caller wants to invoke `Verify` directly.

`AddPin` takes flexible input. The accepted forms are `sha256/...` prefixes, raw hex (64 contiguous chars, no spaces), and raw base64. The lib normalises everything down to base64 internally:

```go
p.AddPin("sha256/YSxNUV05SLc2H4Z6kOXWCsUPPMenylyBVtogFlUiByE=")  // works (with prefix)
p.AddPin("612c4d515d3948b7361f867a90e5d60ac50f3cc7a7ca5c8156da201655220721")  // works (hex, contiguous)
p.AddPin("YSxNUV05SLc2H4Z6kOXWCsUPPMenylyBVtogFlUiByE=")  // works (raw base64)
```

Hex must be a single contiguous 64-character string. The normaliser strips the `sha256/` and `sha256:` prefixes, then converts a 64-char hex string to base64. It does NOT strip whitespace; a pin with spaces in the middle (`612c4d51 5d3948b7 ...`) lands in the pin set as the literal spaced string and never matches a real SPKI hash. Strip whitespace yourself before calling `AddPin`.

## Pin scoping with PinOption

Pins default to "all hosts", which is almost always wrong. Two options narrow scope:

| Option | Effect |
|---|---|
| `client.ForHost("example.com")` | Pin only fires when `host == "example.com"` |
| `client.IncludeSubdomains()` | Pin also fires for `*.example.com` (used together with `ForHost`) |

Combine them:

```go
c.PinCertificate(spkiHash,
    client.ForHost("example.com"),
    client.IncludeSubdomains(),
)
```

Skip both options and the pin applies globally. Every TLS connection through the client checks against it, which is almost never the intended behavior.

## Pin failure handling

When verification fails, the returned error is a `*client.CertPinError` carrying the host and both sides of the mismatch:

```go
resp, err := c.Do(ctx, req)
if err != nil {
    var pinErr *client.CertPinError
    if errors.As(err, &pinErr) {
        fmt.Printf("pin failure on %s\n", pinErr.Host)
        fmt.Printf("expected: %v\n", pinErr.ExpectedHashes)
        fmt.Printf("actual:   %v\n", pinErr.ActualHashes)
    }
}
```

The `ActualHashes` list contains the SPKI hash of every cert in the peer chain, leaf first. Handy for figuring out whether the wrong cert showed up or whether the right cert just rotated to a new key.

## Computing pins from inside Go

The shell pipeline below works for one-off captures. For programmatic pin capture (a daemon that re-pins as certs rotate, a test that grabs the live SPKI before pinning), the `client` package exposes the helper directly:

```go
import "github.com/sardanioss/httpcloak/client"

hash := client.CalculateSPKIHash(cert)            // *x509.Certificate -> base64 SPKI hash
```

The hash is SHA-256 of the certificate's `RawSubjectPublicKeyInfo`, base64-encoded with standard padding. That's the same value the openssl pipeline produces, byte for byte. For a chain, loop and call `CalculateSPKIHash` per cert; the lib doesn't ship a public chain helper today.

Inspecting an active pinner's state is a single call:

```go
pins := c.CertPinner().GetPins()        // []*client.CertificatePin
for _, p := range pins {
    fmt.Printf("host=%s subdomains=%v hash=%s type=%v\n",
        p.Host, p.IncludeSubdomains, p.Hash, p.Type)
}
```

`CertificatePin` carries `Hash`, `Host`, `IncludeSubdomains`, and `Type` (a `client.PinType` enum). The two values today are `PinTypeSHA256` (the default; SHA-256 of the SPKI as set by `AddPin` and `PinCertificate`) and `PinTypeCertificate` (reserved for whole-cert pinning, not yet wired into the AddPin/PinCertificate flow).

## How to capture a pin from the shell

The one-liner. Pipe the cert into openssl, extract the public key, hash the DER, base64-encode it:

```bash
echo | openssl s_client -servername example.com -connect example.com:443 2>/dev/null \
  | openssl x509 -pubkey -noout \
  | openssl pkey -pubin -outform DER \
  | openssl dgst -sha256 -binary \
  | base64
```

Output (example.com, captured 2026-05-10):

```
YSxNUV05SLc2H4Z6kOXWCsUPPMenylyBVtogFlUiByE=
```

That's the value to feed `PinCertificate`. Run the pipeline once per target, stash the hash somewhere, ship it.

## End-to-end example

This Go program captures example.com's SPKI on the fly via openssl, pins it, confirms the request lands, then swaps in a bogus pin and checks that verification fails:

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "io"
    "os/exec"
    "strings"

    "github.com/sardanioss/httpcloak/client"
)

func captureSPKI(host string) (string, error) {
    cmd := exec.Command("bash", "-c", fmt.Sprintf(
        `echo | openssl s_client -servername %s -connect %s:443 2>/dev/null `+
            `| openssl x509 -pubkey -noout `+
            `| openssl pkey -pubin -outform DER `+
            `| openssl dgst -sha256 -binary `+
            `| base64`, host, host))
    out, err := cmd.Output()
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(out)), nil
}

func main() {
    ctx := context.Background()
    host := "example.com"

    spki, err := captureSPKI(host)
    if err != nil {
        panic(err)
    }
    fmt.Printf("captured SPKI: %s\n", spki)

    // Pin the real hash, request should succeed
    c := client.NewClient("chrome-latest")
    c.PinCertificate(spki, client.ForHost(host))

    req := &client.Request{Method: "GET", URL: "https://" + host + "/"}
    resp, err := c.Do(ctx, req)
    if err != nil {
        panic(err)
    }
    io.Copy(io.Discard, resp.Body)
    resp.Body.Close()
    fmt.Printf("pinned request: status=%d\n", resp.StatusCode)

    // Swap to a bogus pin, request should fail with CertPinError
    c.ClearPins()
    c.PinCertificate("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", client.ForHost(host))

    _, err = c.Do(ctx, req)
    var pinErr *client.CertPinError
    if errors.As(err, &pinErr) {
        fmt.Printf("pin failure on %s\n", pinErr.Host)
        fmt.Printf("expected: %v\n", pinErr.ExpectedHashes)
        fmt.Printf("got:      %v\n", pinErr.ActualHashes)
    } else {
        fmt.Println("expected CertPinError, got:", err)
    }
}
```

</TabItem>
<TabItem value="python" label="Python">

Pinning is Go-only right now. The Python binding doesn't surface `PinCertificate` yet. To use pinning from Python, run a local httpcloak proxy with pinning configured on the Go side and point Python at it. Open a GH issue if you want to bump priority on the Python binding.

</TabItem>
<TabItem value="node" label="Node.js">

Same as Python. The Node binding doesn't expose pin APIs yet. Wrap a Go-side httpcloak local proxy with pins enforced and route Node traffic through it.

</TabItem>
<TabItem value="dotnet" label=".NET">

.NET binding doesn't expose pin APIs yet either. Same workaround applies: Go-side local proxy with pins, .NET points at it.

</TabItem>
</Tabs>

Sample output, run against example.com on 2026-05-10:

```text
captured SPKI: YSxNUV05SLc2H4Z6kOXWCsUPPMenylyBVtogFlUiByE=
pinned request: status=200
pin failure on example.com
expected: [AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=]
got:      [YSxNUV05SLc2H4Z6kOXWCsUPPMenylyBVtogFlUiByE= Kt2bkYM55rPaGBFYxTLlq8AIJqapRcc1eKjai8GUPO0= OXyj9ngbqO9cjLeO/+t9Ggl2EP4JTnVWHq4LEwhFM9w= G/ANXI8TwJTdF+AFBM8IiIUPEv0Gf6H5LA/b9guG4yE=]
```

First request: 200, pin matched. Second: `CertPinError`, with peer chain hashes surfaced in `ActualHashes` so the exact set of certs on the wire is visible.

:::warning
Pins go stale. Sites rotate certs, sometimes on a schedule (Let's Encrypt is 90 days), sometimes after an incident, and a hardcoded SPKI hash dies the moment the keypair changes. Build a refresh path: re-capture the hash on a cron, pin multiple SPKIs (current + next) at once, or fall back gracefully when `CertPinError` shows up. A pinned client that fails 100% after cert rotation is worse than no pin at all.
:::
