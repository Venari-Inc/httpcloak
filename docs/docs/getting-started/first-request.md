---
title: First Request
sidebar_position: 2
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# First Request

httpcloak emits the same wire bytes as a real browser across the TLS ClientHello, HTTP/2 SETTINGS frame, header order, and priority frames. A site that fingerprints clients sees Chrome (or Firefox, or Safari), not Go's `net/http` or Python `requests`.

This page is the four-line check that the install works. Pick a language, run the snippet, and you should get a 200 from `tls.peet.ws/api/all` with a Chrome-shaped fingerprint in the body.

## The snippet

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sardanioss/httpcloak"
)

func main() {
	sess := httpcloak.NewSession("chrome-latest",
		httpcloak.WithSessionTimeout(30*time.Second),
	)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := sess.Get(ctx, "https://tls.peet.ws/api/all")
	if err != nil {
		panic(err)
	}
	defer resp.Close()

	fmt.Println("status:", resp.StatusCode)
	fmt.Println("protocol:", resp.Protocol)

	body, _ := resp.Text()
	fmt.Println(body)
}
```

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

with httpcloak.Session(preset="chrome-latest", timeout=30) as session:
    r = session.get("https://tls.peet.ws/api/all")
    print("status:", r.status_code)
    print("protocol:", r.protocol)
    print(r.text)
```

</TabItem>
<TabItem value="node" label="Node.js">

```javascript
const { Session } = require("httpcloak");

(async () => {
  const session = new Session({ preset: "chrome-latest", timeout: 30 });
  try {
    const r = await session.get("https://tls.peet.ws/api/all");
    console.log("status:", r.statusCode);
    console.log("protocol:", r.protocol);
    console.log(r.text);
  } finally {
    session.close();
  }
})();
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var session = new Session(preset: "chrome-latest", timeout: 30);
var r = session.Get("https://tls.peet.ws/api/all");
Console.WriteLine($"status: {r.StatusCode}");
Console.WriteLine($"protocol: {r.Protocol}");
Console.WriteLine(r.Text);
```

</TabItem>
</Tabs>

## What you should see

The full response is a JSON blob covering TLS, HTTP/2, and header data. The parts that matter, trimmed down:

```json
{
  "http_version": "h2",
  "tls": {
    "ja3_hash": "55ecc08008f90a8b2a5c5289ab0f8b69",
    "ja4": "t13d1516h2_8daaf6152771_d8a2da3f94cd"
  },
  "http2": {
    "akamai_fingerprint": "1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p",
    "akamai_fingerprint_hash": "52d84b11737d980aef856699f885ca86"
  },
  "user_agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
}
```

A few things worth flagging:

- `http_version` is `h2`. Chrome negotiates HTTP/2 against any ALPN-capable server, and httpcloak follows the same pattern. HTTP/3 takes over when the server advertises it via Alt-Svc. To skip negotiation, pin a transport with `WithForceHTTP2()` or `WithForceHTTP3()`.
- `ja4` stays stable across runs on the same preset. `ja3_hash` does not, because Chrome shuffles GREASE extension values on every ClientHello and the JA3 string folds those values in. JA4 strips GREASE before hashing. Match against JA4 and ignore JA3.
- `akamai_fingerprint_hash` rolls H2 SETTINGS, WINDOW_UPDATE, PRIORITY, and pseudo-header order into one value. It should match what real Chrome 148 ships.

:::tip tls.peet.ws is your friend
Bookmark `tls.peet.ws/api/all`. Any time a preset gets tweaked, a custom JA3 goes in, or a target keeps flagging the request, hit this endpoint and diff the response against a real browser. DevTools doesn't expose request header order, so this endpoint is the closest thing to a source of truth.
:::

## Where to next

- [Presets Explained](./presets-explained) for what `chrome-latest` bundles and how to pick something else.
- [Common Options](./common-options) for timeouts, retries, redirects, and the rest of the everyday surface.
- [Fingerprinting overview](/fingerprinting) for hand-tuning the wire bytes.
