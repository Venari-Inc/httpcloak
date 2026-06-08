---
title: JSON Bodies
sidebar_position: 3
---

# JSON Bodies

Most APIs speak JSON. Send a body, parse a body. The shortcut wrappers handle serialization and Content-Type for you in every binding except Go, where the API stays explicit so you can stream large payloads instead of buffering them whole.

## Sending JSON

Pass a struct (Go) or a dict / object (Python, Node, .NET). The lib serializes it, sets `Content-Type: application/json`, and ships it out.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs groupId="lang">
<TabItem value="go" label="Go">

In Go you build the body yourself with `encoding/json` and pass an `io.Reader`.

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"

    httpcloak "github.com/sardanioss/httpcloak"
)

func main() {
    s := httpcloak.NewSession("chrome-latest")
    defer s.Close()

    payload := map[string]any{"hello": "world", "n": 42}
    body, _ := json.Marshal(payload)

    req := &httpcloak.Request{
        Method: "POST",
        URL:    "https://httpbin.org/post",
        Headers: map[string][]string{
            "Content-Type": {"application/json"},
        },
        Body: bytes.NewReader(body),
    }
    resp, _ := s.Do(context.Background(), req)
    defer resp.Close()

    var parsed struct {
        JSON map[string]any `json:"json"`
    }
    resp.JSON(&parsed)
    fmt.Println(parsed.JSON) // map[hello:world n:42]
}
```

The non-session API has a shortcut too. `httpcloak.Client` (the alternate, stateless API in the top-level package) exposes `PostJSON(ctx, url, body)` which sets the Content-Type for you. Construct it with `httpcloak.New(preset, opts...)`.

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

s = httpcloak.Session(preset="chrome-latest")

r = s.post(
    "https://httpbin.org/post",
    json={"hello": "world", "n": 42},
)

print(r.json()["json"])  # {"hello": "world", "n": 42}
```

The `json=` kwarg handles serialization and sets `Content-Type: application/json`. If the body is already serialized, pass it via `data=` and set the header yourself.

</TabItem>
<TabItem value="nodejs" label="Node.js">

```js
const { Session } = require("httpcloak");

const s = new Session({ preset: "chrome-latest" });

const r = await s.post("https://httpbin.org/post", {
  json: { hello: "world", n: 42 },
});

console.log(r.json().json); // { hello: "world", n: 42 }
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(preset: "chrome-latest");

var payload = new { hello = "world", n = 42 };
var r = s.PostJson("https://httpbin.org/post", payload);

Console.WriteLine(r.Text);
```

`PostJson` uses `System.Text.Json` under the hood and sets the Content-Type header for you.

</TabItem>
</Tabs>

## Reading JSON

Responses come back as bytes you parse on demand.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
resp, _ := s.Get(ctx, "https://httpbin.org/json")
defer resp.Close()

var data struct {
    Slideshow struct {
        Title string `json:"title"`
    } `json:"slideshow"`
}
if err := resp.JSON(&data); err != nil {
    // not valid JSON, or read error
}
fmt.Println(data.Slideshow.Title)
```

`resp.JSON(&v)` reads the full body and unmarshals into `v`. The body buffers after the first read, so calling `resp.Bytes()` or `resp.Text()` afterward returns the same payload.

</TabItem>
<TabItem value="python" label="Python">

```python
r = s.get("https://httpbin.org/json")
data = r.json()
print(data["slideshow"]["title"])
```

</TabItem>
<TabItem value="nodejs" label="Node.js">

```js
const r = await s.get("https://httpbin.org/json");
const data = r.json();
console.log(data.slideshow.title);
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
var r = s.Get("https://httpbin.org/json");
using var doc = JsonDocument.Parse(r.Text);
var title = doc.RootElement
    .GetProperty("slideshow")
    .GetProperty("title")
    .GetString();
```

</TabItem>
</Tabs>

## Pitfalls worth knowing

### Content-Type sniffing

A body sent with no `Content-Type` may get sniffed downstream and labelled as something you didn't intend. JSON requests must carry `Content-Type: application/json`, otherwise:

- Some servers treat the body as form data and 400 you back.
- Some WAFs flag the request as suspicious.

The shortcut wrappers (`PostJson`, `post(json=...)`, etc.) set this for you. Building the request by hand puts the responsibility on the caller.

### Encoding

JSON is always UTF-8 on the wire. Don't try Latin-1 or UTF-16 even if the server claims it would accept either. Browsers send UTF-8 and anti-bot products expect UTF-8.

### Big responses

For multi-MB or larger payloads, don't buffer the whole body. Use the streaming API instead, covered in [Streaming Responses](./streaming-responses).

`resp.JSON()` and `resp.Bytes()` both pull the entire body into memory. A 200MB JSON dump through that path hurts.

### Numbers

In Go, JSON numbers default to `float64`. Integer IDs over 2^53 (snowflake IDs and the like) need `json.Number` or string encoding to round-trip cleanly. Python and Node native ints don't have this issue, and .NET's `JsonElement.GetInt64()` handles 64-bit cleanly.

### Trailing newlines, BOMs

httpbin and most servers don't send these, but a server that prefixes its JSON with a UTF-8 BOM (`\xef\xbb\xbf`) will choke most parsers. Strip the BOM before parsing. Rare, but it shows up on some old Java backends.

## A test pattern that works

The httpbin echo endpoints verify whether the client serializes what you expect:

- `POST https://httpbin.org/post` echoes the request as JSON, including a parsed `json` field if you sent JSON.
- `PUT https://httpbin.org/put`, `DELETE https://httpbin.org/delete`, `PATCH https://httpbin.org/patch` do the same for their methods.

If the client should be sending `{"hello": "world"}`, hit `/post` and check that `response.json["hello"] == "world"`. Missing or the wrong type means the lib didn't serialize what you thought, or the Content-Type was off.
