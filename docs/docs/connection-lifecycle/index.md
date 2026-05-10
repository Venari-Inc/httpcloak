---
title: Connection Lifecycle
sidebar_position: 1
---

# Connection Lifecycle

A session isn't a one-shot object. It opens connections, caches tickets, accumulates cookies, hops protocols, gets persisted to disk, fans out into siblings. This section covers each operation that moves a session through those states without rebuilding it from scratch.

## In this section

- [Refresh](./refresh): drop every live connection but keep tickets, cookies and fingerprint state, the way a browser tab reload works
- [Warmup](./warmup): multi-hop browser-style preflight that fetches the page and its subresources before the real request
- [Protocol Switching](./protocol-switching): move between H1, H2 and H3 mid-session, with or without auto-negotiation
- [Session Save and Restore](./session-save-restore): serialize the whole session to JSON, load it back in another process
- [Fork](./fork): N sibling sessions sharing cookies and TLS state, each with its own connection pool
- [Observability](./observability): Stats, IdleTime, Touch, ClearCache, GetTransport for inspecting live session state
