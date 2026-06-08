---
title: Bindings
sidebar_position: 1
---

# Bindings

httpcloak ships in four languages. Go is the native implementation. Python, Node.js, and .NET each call into the same cgo-built shared library, so the wire behaviour matches across all four surfaces.

## In this section

- [Go](./go): the native API, idiomatic Go
- [Python](./python): a `requests`-shaped wrapper over cgo
- [Node.js](./nodejs): koffi-backed, ESM and CJS both work
- [.NET](./dotnet): P/Invoke wrapper for .NET 8+
