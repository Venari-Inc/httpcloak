---
title: Fork (Sibling Sessions)
sidebar_position: 5
---

# Fork (Sibling Sessions)

`Fork(n)` returns N sibling sessions that share cookies and TLS state with the parent but get their own connection pools. The use case is fanning out work in parallel under one logged-in identity without every worker fighting over the same sockets.

The model is N browser tabs from the same browser window. Same login, same cookie jar, same fingerprint. Each tab opens its own TCP and QUIC connections, so a slow request on one tab doesn't queue requests on the others.

## What's shared

Forked siblings share the live state that defines the identity:

- **Cookie jar.** Same pointer. A `Set-Cookie` from any sibling lands in the jar that every sibling (and the parent) reads from. Log in on the parent, fork, every child is logged in.
- **TLS resumption tickets.** The H1, H2, and H3 session caches are shared (the same `*ClientSessionCache` pointer lives on both transports). The first handshake on a fresh fork resumes from a ticket the parent already cached, so it lands on the 0-RTT path.
- **ECH config (DNS-side).** The DNS HTTPS RR cache (`dns.echCache`) is process-wide, so any fork that dials a host the parent has already resolved skips the HTTPS RR lookup. The per-transport `echConfigCache` (the binding between a host and the exact config used to mint a session ticket) does NOT propagate; each fork builds its own as it dials, and a fork's first connection to a given host does one fresh HTTPS RR cache hit before populating the per-transport map.
- **Custom fingerprint state.** Custom JA3, custom H2 settings, custom pseudo-header order, custom TCP fingerprint, header order. The full fingerprint surface copies over on fork.
- **Cache validators.** ETag and Last-Modified entries snapshot at fork time so siblings start with believable conditional-request headers.

## What's NOT shared

Each fork gets its own:

- **Connection pool.** Fresh transport, new TCP sockets, new QUIC connections. Siblings don't queue behind each other.
- **In-flight requests.** A request on one fork can't block or cancel a request on another.
- **Idle timer and stats.** `LastUsed`, `RequestCount` and idle close timers are per-fork.
- **Key log writer.** Forks don't inherit the parent's TLS keylog, so they can't double-close it. Set up a keylog per fork if needed.

## Common pattern: warm up, log in, fork, scrape

The typical flow is hit the home page on the parent so cookies and tickets land, log in, fork into N workers, hand each worker a slice of URLs.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
s := httpcloak.NewSession("chrome-latest")
defer s.Close()
ctx := context.Background()

// Warm up so the parent has cookies and TLS tickets cached.
if err := s.Warmup(ctx, "https://example.com/"); err != nil {
    panic(err)
}

// Pretend we logged in here. Cookie jar now has the session token.
_, _ = s.Post(ctx, "https://example.com/login", loginBody, nil)

// Fork into 4 siblings. Each gets its own connection pool but
// inherits the cookie jar and TLS tickets from the parent.
forks := s.Fork(4)

urls := []string{
    "https://example.com/page/1",
    "https://example.com/page/2",
    "https://example.com/page/3",
    "https://example.com/page/4",
}

var wg sync.WaitGroup
for i, f := range forks {
    wg.Add(1)
    go func(idx int, fs *httpcloak.Session) {
        defer wg.Done()
        defer fs.Close()
        r, err := fs.Get(ctx, urls[idx])
        if err != nil { return }
        defer r.Close()
        fmt.Printf("worker[%d] status=%d\n", idx, r.StatusCode)
    }(i, f)
}
wg.Wait()
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak
from concurrent.futures import ThreadPoolExecutor

with httpcloak.Session(preset="chrome-latest") as s:
    s.warmup("https://example.com/")
    s.post("https://example.com/login", data=login_body)

    forks = s.fork(4)

    urls = [
        "https://example.com/page/1",
        "https://example.com/page/2",
        "https://example.com/page/3",
        "https://example.com/page/4",
    ]

    def hit(args):
        idx, fs = args
        try:
            r = fs.get(urls[idx])
            print(f"worker[{idx}] status={r.status_code}")
        finally:
            fs.close()

    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(hit, enumerate(forks)))
```

</TabItem>
<TabItem value="nodejs" label="Node.js">

```javascript
const httpcloak = require("httpcloak");

const s = new httpcloak.Session({ preset: "chrome-latest" });
try {
  await s.warmup("https://example.com/");
  await s.post("https://example.com/login", { body: loginBody });

  const forks = s.fork(4);
  const urls = [
    "https://example.com/page/1",
    "https://example.com/page/2",
    "https://example.com/page/3",
    "https://example.com/page/4",
  ];

  await Promise.all(
    forks.map(async (fs, idx) => {
      try {
        const r = await fs.get(urls[idx]);
        console.log(`worker[${idx}] status=${r.statusCode}`);
      } finally {
        fs.close();
      }
    })
  );
} finally {
  s.close();
}
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(preset: "chrome-latest");
s.Warmup("https://example.com/");
s.Post("https://example.com/login", loginBody);

var forks = s.Fork(4);
var urls = new[] {
    "https://example.com/page/1",
    "https://example.com/page/2",
    "https://example.com/page/3",
    "https://example.com/page/4",
};

await Task.WhenAll(forks.Select((fs, idx) => Task.Run(() => {
    try
    {
        var r = fs.Get(urls[idx]);
        Console.WriteLine($"worker[{idx}] status={r.StatusCode}");
    }
    finally
    {
        fs.Dispose();
    }
})));
```

</TabItem>
</Tabs>

Every fork ships requests with the same logged-in cookies, every fork resumes TLS from the parent's tickets, and they all carry an identical JA4 because the fingerprint state copied at fork time. Verified locally with three forks against `tls.peet.ws`, all three returned `t13d1517h2_8daaf6152771_b6f405a00624`.

## Lifecycle

Forks are independent siblings, not children attached to the parent's lifecycle.

- Closing the parent doesn't close the forks. They keep working.
- Closing a fork doesn't affect its siblings. The shared cookie jar stays alive as long as any sibling or the parent holds a reference.
- A `Set-Cookie` on any sibling propagates to every other sibling and the parent immediately. Same pointer.
- Fingerprint state copies once at fork time. Mutating the parent's header order after forking has no effect on existing forks; new forks made after the mutation pick up the new state.

For a fork that should hold its own cookie jar, the right answer isn't a fork. Build a fresh `NewSession`.

## Fork vs LoadSession

Both clone session state, but they solve different problems:

| | `Fork(n)` | `Save` / `LoadSession` |
| --- | --- | --- |
| Where it works | One process | Across processes, machines, restarts |
| Cookie jar | Shared live pointer | Snapshot at save time |
| TLS tickets | Shared live cache | Snapshot, serialised to disk |
| ECH config | Shared live cache | Snapshot, base64 in JSON |
| Connection pools | Independent per fork | New session, fresh pool |
| Use when | Parallel workers in one binary | Persisting login across restarts |

Fork is for live fan-out. LoadSession is for resuming yesterday's session. They don't compete, and the two are often used together: load a saved session at startup, fork it for parallel work, save the parent again at shutdown.

:::tip
Pick a fork count the network can actually feed. 10-50 is the typical sweet spot. Past that, the forks queue against shared resources: same NIC, same DNS resolver, same upstream socket budget. CPU goes into rebuilding TLS handshakes that can't ship fast enough. If the target enforces per-IP rate limits, more forks don't help anyway; that's the cue to put real proxies behind each fork.
:::

## Test it yourself

The test below confirms forks share TLS state. Three forks hit `tls.peet.ws/api/all` in parallel, and all three should return 200 with the same JA4 hash. A JA4 mismatch across forks means the fingerprint state didn't copy, which is a bug.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "sync"

    "github.com/sardanioss/httpcloak"
)

func main() {
    s := httpcloak.NewSession("chrome-latest")
    defer s.Close()
    ctx := context.Background()

    // Warm up the parent so it has TLS tickets to share.
    if _, err := s.Get(ctx, "https://tls.peet.ws/api/all"); err != nil {
        panic(err)
    }

    forks := s.Fork(3)
    results := make([]string, 3)

    var wg sync.WaitGroup
    for i, f := range forks {
        wg.Add(1)
        go func(idx int, fs *httpcloak.Session) {
            defer wg.Done()
            defer fs.Close()
            r, err := fs.Get(ctx, "https://tls.peet.ws/api/all")
            if err != nil { return }
            defer r.Close()
            body, _ := io.ReadAll(r.Body)
            var parsed struct {
                TLS struct{ JA4 string `json:"ja4"` } `json:"tls"`
            }
            _ = json.Unmarshal(body, &parsed)
            results[idx] = fmt.Sprintf("status=%d ja4=%s", r.StatusCode, parsed.TLS.JA4)
        }(i, f)
    }
    wg.Wait()

    for i, r := range results {
        fmt.Printf("fork[%d] %s\n", i, r)
    }
}
```

Sample output (Chrome latest, captured 2026-05):

```text
fork[0] status=200 ja4=t13d1517h2_8daaf6152771_b6f405a00624
fork[1] status=200 ja4=t13d1517h2_8daaf6152771_b6f405a00624
fork[2] status=200 ja4=t13d1517h2_8daaf6152771_b6f405a00624
```

Same JA4 across all three. That's proof TLS fingerprint state shares on fork, not just the cookie jar.
