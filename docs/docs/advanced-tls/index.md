---
title: Advanced TLS
sidebar_position: 1
---

# Advanced TLS

This section covers the deeper TLS knobs in httpcloak: ECH, speculative CONNECT, keylogging for Wireshark, domain fronting, certificate pinning, certificate-verification overrides, and distributed session caching. Most sessions never touch any of these. The ones that do tend to need them sharply, so each chapter is self-contained.

## In this section

- [ECH](./ech): Encrypted Client Hello. On by default, opt out with `WithDisableECH`.
- [Speculative TLS](./speculative-tls): pipeline CONNECT and ClientHello, save one RTT on every proxied dial.
- [TLS Keylog](./tls-keylog): dump `SSLKEYLOGFILE` for Wireshark when you need to see what's actually on the wire.
- [Domain Fronting](./domain-fronting): when SNI isn't Host, here's how to wire it up.
- [Cert Pinning](./cert-pinning): pin a server's certificate or public key at the application layer, on top of the system trust store.
- [Insecure Skip Verify](./insecure-skip-verify): skip certificate verification for self-signed dev certs and MITM-proxy testing. Never in production.
- [Session Cache](./session-cache): plug a Redis (or anything) backend into the TLS ticket cache so resumption state spreads across replicas.

## TLS-only mode

`WithTLSOnly()` keeps the preset's TLS fingerprint (uTLS handshake, cipher list, extension order, ALPN, supported groups) but suppresses the preset's default HTTP headers. You set every header per request and the lib stops injecting User-Agent, sec-ch-ua, Accept-Language, and the rest of the Chrome header bundle. Use this when your code already produces authentic browser headers and you only want httpcloak's TLS layer underneath, the canonical case being Playwright driving a real Chrome with Node's Undici sitting under it. For the LocalProxy variant of the same idea, see the [TLSOnly mode section](../recipes/local-proxy-server#tlsonly-mode) in the Local Proxy Server chapter.

```go
s := httpcloak.NewSession("chrome-latest", httpcloak.WithTLSOnly())
defer s.Close()

req := &httpcloak.Request{
    Method: "GET",
    URL:    "https://example.com/",
    Headers: map[string][]string{
        "User-Agent": {"your-own-UA-bundle/1.0"},
        "Accept":     {"text/html,application/xhtml+xml,*/*;q=0.8"},
    },
}
// every header set by you, none from the preset
resp, _ := s.Do(context.Background(), req)
defer resp.Close()
```

The flag is exposed as `tls_only=True` (Python), `tlsOnly: true` (Node), and `tlsOnly: true` (.NET) on the `Session` constructor.
