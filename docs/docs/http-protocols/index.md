---
title: HTTP Protocols
sidebar_position: 1
---

# HTTP Protocols

H1, H2, H3, and how the lib decides which one to use.

## In this section

- [HTTP/1.1](./http1): when H1 is forced, what gets negotiated, and the rare cases where it shows up.
- [HTTP/2](./http2): the default for most modern hosts. How SETTINGS and PRIORITY look on the wire.
- [HTTP/3 (QUIC)](./http3-quic): QUIC over UDP, why we ride sardanioss/quic-go, what 0-RTT gets you.
- [Auto-Negotiation](./auto-negotiation): how the lib picks H1 vs H2 vs H3, and how to force one.
