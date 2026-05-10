---
title: Hooks
sidebar_position: 7
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Hooks

Hooks are middleware. PreRequest fires right before a request hits the wire, PostResponse fires right after the response lands. Same idea as Express middleware or `requests.Session.hooks`, with httpcloak's types. Use them to mutate, log, transform, or kill a request at the gate.

Two slots:

- **PreRequest**: gets the outgoing request. Mutate headers, attach a request ID, or return an error to abort.
- **PostResponse**: gets the parsed response. Inspect status, pull a token out of a header, log timing.

## PreRequestHook

```go
type PreRequestHook func(req *http.Request) error
```

The hook gets the actual `*http.Request` (the `sardanioss/http` one) right before the transport ships it. Anything you change lands on the wire: headers, URL, method, body. A non-nil return cancels the request before a single byte goes out, and the error bubbles back to the caller wrapped as `pre-request hook failed: <your err>`.

Typical uses:

- Inject a correlation header like `X-Request-ID` or a tracing span ID
- Refresh a bearer token if it's near expiry
- Block the request based on a URL allowlist (return an error)
- Log the outbound URL and method for telemetry

## PostResponseHook

```go
type PostResponseHook func(resp *Response) error
```

Fires once the response is fully built (status, headers, body buffered or streaming). The hook can read `resp.StatusCode`, `resp.Headers`, `resp.Timing`, `resp.Protocol`, and the rest of the response surface. Returning an error here is advisory only. Hooks are observability, not control flow, so the response flows back to the caller untouched. To fail loud on a 5xx, do it at the call site, not the hook.

Typical uses:

- Log status + timing for every response
- Extract a `X-Auth-Token` from response headers and stash it
- Warn or page on 4xx / 5xx clusters
- Buffer the body for a debug capture (call `resp.Bytes()` inside the hook)

## Order and chaining

Hooks fire in the order you registered them. Three PreRequest hooks run 1, 2, 3 on every request. PreRequest hooks short-circuit on the first error: if hook 2 returns an error, hook 3 never fires and the request never goes out.

PostResponse runs the same order with the same short-circuit behaviour. The first hook returning an error stops the chain and surfaces the error back to the request caller.

## Wiping hooks

`ClearHooks()` drops everything. Useful between test cases when you want a clean slate.

```go
c.ClearHooks()
```

There's also `c.Hooks().ClearPreRequest()` and `c.Hooks().ClearPostResponse()` for clearing one side at a time.

## Example: log every URL, warn on errors

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
package main

import (
    "context"
    "fmt"

    http "github.com/sardanioss/http"
    "github.com/sardanioss/httpcloak/client"
)

func main() {
    c := client.NewClient("chrome-latest")
    defer c.Close()

    c.OnPreRequest(func(req *http.Request) error {
        fmt.Printf("[pre] %s %s\n", req.Method, req.URL)
        req.Header.Set("X-Request-ID", "abc-123")
        return nil
    })

    c.OnPostResponse(func(resp *client.Response) error {
        if resp.StatusCode >= 400 {
            fmt.Printf("[post] WARN %d %s\n", resp.StatusCode, resp.FinalURL)
        } else {
            fmt.Printf("[post] ok   %d %s\n", resp.StatusCode, resp.FinalURL)
        }
        return nil
    })

    resp, _ := c.Get(context.Background(), "https://httpbin.org/get", nil)
    resp.Close()

    resp, _ = c.Get(context.Background(), "https://httpbin.org/status/418", nil)
    resp.Close()
}
```

</TabItem>
</Tabs>

The Python, Node, and .NET bindings do not currently expose the hook surface (no `Client` class, no `on_pre_request` / `onPreRequest` / `OnPreRequest` methods). For binding callers, the equivalent pattern is wrapping the binding's `Session` in your own thin class that runs your callbacks before and after every `get`/`post`/`request` call. A two-line wrapper covers the common cases (logging, header injection, response tagging) without needing lib-side hook support.

What you'll see on stdout:

```text
[pre] GET https://httpbin.org/get
[post] ok   200 https://httpbin.org/get
[pre] GET https://httpbin.org/status/418
[post] WARN 418 https://httpbin.org/status/418
```

Two requests, four hook fires. The pre hook stamped both with `X-Request-ID: abc-123`, the post hook flagged the teapot.

:::warning
Hooks run on the request's hot path, synchronously, every single request. A hook that does network I/O or grabs a contended mutex tanks throughput. If your hook needs to hit a database or push to a queue, do it on a background goroutine / async task and return immediately. Heavy hooks turn a 50ms request into a 500ms one, and the cause is hard to spot from the call site.
:::

## Pulling a token out of a response

A quick pattern. The server sets `X-Auth-Token` on login. Stash it from a hook so the rest of the code can grab it without parsing every response by hand.

```go
var token string

c.OnPostResponse(func(resp *client.Response) error {
    if t := resp.GetHeader("X-Auth-Token"); t != "" {
        token = t
    }
    return nil
})
```

`GetHeader` is case-insensitive, so the lookup matches whether the server sent `X-Auth-Token` or `x-auth-token`.

## Blocking a request at the gate

Returning an error from PreRequest cancels the request before it goes out. Useful for kill-switches or allowlists.

```go
allowed := map[string]bool{"api.example.com": true}

c.OnPreRequest(func(req *http.Request) error {
    if !allowed[req.URL.Host] {
        return fmt.Errorf("host %q not on allowlist", req.URL.Host)
    }
    return nil
})
```

The caller sees `pre-request hook failed: host "evil.example.com" not on allowlist` and zero bytes hit the wire.
