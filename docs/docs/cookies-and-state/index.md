---
title: Cookies & State
sidebar_position: 1
---

# Cookies & State

Every `Session` ships with a built-in cookie jar that stores `Set-Cookie` values from responses and replays the matching ones on follow-up requests. This section walks through how the jar decides what to store and what to send, how to switch it off when you'd rather drive cookies yourself, how to attach a `Cookie` header to a single request, and the domain and path rules that decide whether a stored cookie rides along on the next call.

## In this section

- [Cookie Jar](./cookie-jar): how the internal jar works, what gets stored, when it sends what
- [Disabling the Cookie Jar](./disabling-cookie-jar): WithoutCookieJar() and when to reach for it
- [Per-Request Cookies](./per-request-cookies): ad-hoc Cookie headers for one-off calls
- [Domain and Path Matching](./domain-and-path-matching): the rules cookies follow when matching the next URL
