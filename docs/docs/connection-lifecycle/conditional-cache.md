---
title: Conditional Cache
sidebar_position: 9
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Conditional Cache (ETag / If-Modified-Since)

The session keeps a per-URL record of every `ETag` and `Last-Modified` header it sees and replays them on the next request to the same URL as `If-None-Match` / `If-Modified-Since`. That's what a real browser does, and it's how a fresh fingerprint stays believable on a target that profiles cache behaviour.

Three knobs control the feature:

- A constructor-time switch that turns it off for the whole lifetime of the session.
- A runtime toggle that flips the same state on or off without restarting.
- A per-request override that skips the cache for one specific request without touching the session-wide setting.
- A `ClearCache()` method that wipes the stored validators without disabling the feature.

The defaults are browser-shaped: the cache is on, validators are replayed automatically, and the lib never re-asks for the same resource if the server still says `304 Not Modified`.

## When the cache helps

The cache makes the request shape match a real browser. A returning visit to a page they've seen ships an `If-None-Match` on every conditional resource (HTML, CSS, JS, fonts, sometimes images). A scraper that skips these headers stands out the moment it loads a page twice from the same session.

Leave it on by default. Disable it only when the workflow has a reason.

## When to turn it off

Common reasons:

- Benchmarking or load-testing: you want every request to hit the origin fresh, not return `304 Not Modified` from the second hit onwards.
- Fingerprint capture work: you're comparing the wire bytes between two consecutive identical sessions and the validator headers add noise.
- Caching layer testing: you're inspecting how a CDN behaves with the cache headers stripped.
- A target that rejects conditional requests entirely or returns wrong content on `304`.

The lib stays silent about the cache otherwise. The validators are real headers on real responses, and the choice to send them on the next request is a pure session-state decision.

## Constructor-time off

Disable the whole feature when the session is built. Every subsequent request goes out without cache headers and no validator is stored from any response.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
s := httpcloak.NewSession("chrome-latest",
    httpcloak.WithoutConditionalCache(),
)
defer s.Close()
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

with httpcloak.Session(preset="chrome-latest", without_conditional_cache=True) as s:
    r = s.get("https://example.com/")
```

</TabItem>
<TabItem value="nodejs" label="Node.js">

```js
const { Session } = require("httpcloak");

const s = new Session({ preset: "chrome-latest", withoutConditionalCache: true });
const r = await s.get("https://example.com/");
s.close();
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(
    preset: "chrome-latest",
    withoutConditionalCache: true);

var r = s.Get("https://example.com/");
```

</TabItem>
</Tabs>

## Runtime toggle

Flip the feature on or off mid-session. The change applies to the next request. Existing stored validators are preserved when the feature is disabled, so re-enabling resumes using them. Use `ClearCache()` if you want to wipe the validators too.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
s := httpcloak.NewSession("chrome-latest")
defer s.Close()

s.SetConditionalCacheEnabled(false)
_, _ = s.Get(ctx, "https://example.com/asset")

s.SetConditionalCacheEnabled(true)   // back on, with previously-stored validators

s.ClearCache()                       // wipe stored validators
on := s.ConditionalCacheEnabled()    // read current state
```

</TabItem>
<TabItem value="python" label="Python">

```python
s.set_conditional_cache(False)
r = s.get("https://example.com/asset")

s.set_conditional_cache(True)
s.clear_cache()
on = s.get_conditional_cache()
```

</TabItem>
<TabItem value="nodejs" label="Node.js">

```js
s.setConditionalCache(false);
await s.get("https://example.com/asset");

s.setConditionalCache(true);
s.clearCache();
const on = s.getConditionalCache();
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
session.SetConditionalCache(false);
session.Get("https://example.com/asset");

session.SetConditionalCache(true);
session.ClearCache();
bool on = session.GetConditionalCache();
```

</TabItem>
</Tabs>

## Per-request override

When the session-wide setting is fine and only one or two requests need to bypass the cache, the per-request flag is the right tool. It doesn't touch the session state, it doesn't affect other requests, and the cache map continues to grow / be consulted for every other call. Both `allowRedirects` and `disableConditionalCache` are exposed on every request method in every binding, sync and async alike.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
resp, err := s.Do(ctx, &httpcloak.Request{
    Method:                  "GET",
    URL:                     "https://example.com/page",
    DisableConditionalCache: true,        // skip ETag / If-Modified-Since for this call
    FollowRedirects:         &noFollow,   // (*bool) per-request redirect override
})
```

</TabItem>
<TabItem value="python" label="Python">

```python
# Any request method: get, post, put, delete, patch, head, options, request
# and their *_async siblings.
r = s.get("https://example.com/page", disable_conditional_cache=True)
r = s.get("https://example.com/redirect", allow_redirects=False)
r = await s.post_async("https://example.com/api", json_data={"x": 1},
                       disable_conditional_cache=True,
                       allow_redirects=False)
```

</TabItem>
<TabItem value="nodejs" label="Node.js">

```js
// Any request method: get, post, put, delete, patch, head, options, request,
// getSync, postSync, requestSync, getStream, postStream, requestStream.
const r = await s.get("https://example.com/page", { disableConditionalCache: true });
const r2 = await s.get("https://example.com/redirect", { allowRedirects: false });
const r3 = await s.post("https://example.com/api", {
    json: { x: 1 },
    disableConditionalCache: true,
    allowRedirects: false,
});
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
// Every Get/Post/Put/Delete/Patch/Head/Options + *Async takes the two kwargs.
var r = session.Get("https://example.com/page", disableConditionalCache: true);
var r2 = session.Get("https://example.com/redirect", allowRedirects: false);
var r3 = await session.PostJsonAsync("https://example.com/api", new { x = 1 },
    disableConditionalCache: true,
    allowRedirects: false);
```

</TabItem>
</Tabs>

When both `allowRedirects` and the session-level setting disagree, the per-request value wins for that one call. When `disableConditionalCache` is true for a single call, neither validator-injection nor validator-storage happens for that request; the session's stored map is preserved.

## ClearCache vs SetConditionalCache

The two methods are independent and address different needs:

| Method | Effect on stored validators | Effect on future requests |
|---|---|---|
| `ClearCache()` | Wipes the cache map | Still injects validators going forward (once new ones get stored) |
| `SetConditionalCache(false)` | Preserved as-is | No injection, no storage; effectively pause |
| `SetConditionalCache(false)` + `ClearCache()` | Wiped | Paused |
| `SetConditionalCache(true)` (after pause) | Resumed; uses any preserved entries | Resumes injection and storage |

Combine them for a hard reset: pause, wipe, resume.

## Interaction with `Refresh()`

A `session.Refresh()` call drops live connections but keeps the cookie jar, TLS tickets, and the conditional-cache map. The next request after `Refresh()` adds `cache-control: max-age=0` (mimicking a browser F5) but still sends the stored ETag / If-Modified-Since validators. That combination is the realistic browser-refresh shape: the client asks the cache to revalidate, and the validators are how it does that.

If you want a refresh that also forces a full re-fetch, call `ClearCache()` after `Refresh()`.

## How it works under the hood

The cache lives on `session.Session.cacheEntries` as a `map[string]*cacheEntry` keyed by request URL. `storeCacheHeaders` (`session/session.go:688`) extracts `ETag` and `Last-Modified` from every response. The request path at `session/session.go:250` injects them on the next request to the same URL. Both writes and reads honour the `conditionalCacheEnabled` field and the per-request `DisableConditionalCache` flag.

The cache is per-session. Forks share the cookie jar but get their own conditional-cache map. Cross-session sharing is not exposed today and isn't a goal; the validators are a browser-state shape, not a multi-replica shape.
