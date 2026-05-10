---
title: Form Data and Multipart
sidebar_position: 4
---

# Form Data and Multipart

Two flavors of form posting. Pick based on the payload:

- **`application/x-www-form-urlencoded`** for plain key/value text fields. Small, simple, no binary.
- **`multipart/form-data`** for file uploads, mixed text + file fields, or anything binary.

Browsers pick the same way: `<form>` with `enctype="multipart/form-data"` for uploads, default url-encoded otherwise.

## URL-encoded

The body is `key1=val1&key2=val2` with values percent-encoded, and the Content-Type is `application/x-www-form-urlencoded`.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs groupId="lang">
<TabItem value="go" label="Go">

Go gives you `net/url.Values` to build the body, then ships the bytes.

```go
package main

import (
    "bytes"
    "context"
    "fmt"
    "net/url"

    httpcloak "github.com/sardanioss/httpcloak"
)

func main() {
    s := httpcloak.NewSession("chrome-latest")
    defer s.Close()

    form := url.Values{}
    form.Set("user", "alice")
    form.Set("token", "abc 123")

    req := &httpcloak.Request{
        Method: "POST",
        URL:    "https://httpbin.org/post",
        Headers: map[string][]string{
            "Content-Type": {"application/x-www-form-urlencoded"},
        },
        Body: bytes.NewReader([]byte(form.Encode())),
    }
    resp, _ := s.Do(context.Background(), req)
    defer resp.Close()

    body, _ := resp.Text()
    fmt.Println(body) // "form": {"user": "alice", "token": "abc 123"}
}
```

The non-session API has `httpcloak.Client.PostForm(ctx, url, body)` (the alternate, stateless top-level API; build with `httpcloak.New(preset, opts...)`). It sets the Content-Type for you.

</TabItem>
<TabItem value="python" label="Python">

```python
import httpcloak

s = httpcloak.Session(preset="chrome-latest")

r = s.post(
    "https://httpbin.org/post",
    data={"user": "alice", "token": "abc 123"},
)

print(r.json()["form"])  # {"user": "alice", "token": "abc 123"}
```

When `data=` is a dict, the binding url-encodes it and sets the Content-Type for you. A string or bytes value gets shipped as-is with whatever Content-Type you put in `headers`.

</TabItem>
<TabItem value="nodejs" label="Node.js">

```js
const { Session } = require("httpcloak");

const s = new Session({ preset: "chrome-latest" });

const r = await s.post("https://httpbin.org/post", {
  data: { user: "alice", token: "abc 123" },
});

console.log(r.json().form);
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(preset: "chrome-latest");

var formData = new Dictionary<string, string> {
    { "user", "alice" },
    { "token", "abc 123" }
};
var r = s.PostForm("https://httpbin.org/post", formData);
Console.WriteLine(r.Text);
```

</TabItem>
</Tabs>

Things to keep in mind:

- **Encoding is fixed.** Always UTF-8 percent-encoded. Don't try Latin-1 in form bodies even if a server claims it would accept it.
- **Repeated keys.** url-encoded supports the same key twice (`a=1&a=2`). Most builders give you a list-valued helper. In Go: `form.Add("a", "1"); form.Add("a", "2")`.
- **Length limits.** Some servers cap form bodies around 1MB. For more than a kilobyte of text, multipart is usually the better fit. For megabytes, multipart with file fields is the right pick, or just JSON.

## Multipart

Multipart wraps each field in its own MIME part with a boundary string. The Content-Type looks like `multipart/form-data; boundary=----WebKitFormBoundaryAbCdEf123`, and the body looks roughly like:

```
------WebKitFormBoundaryAbCdEf123
Content-Disposition: form-data; name="comment"

hello there
------WebKitFormBoundaryAbCdEf123
Content-Disposition: form-data; name="file"; filename="note.txt"
Content-Type: text/plain

file body bytes
------WebKitFormBoundaryAbCdEf123--
```

Writing this by hand is almost never worth it. The helpers exist for exactly this.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

httpcloak ships `MultipartField` and `BuildMultipart` for the common case.

```go
package main

import (
    "bytes"
    "context"
    "fmt"

    httpcloak "github.com/sardanioss/httpcloak"
)

func main() {
    s := httpcloak.NewSession("chrome-latest")
    defer s.Close()

    fields := []httpcloak.MultipartField{
        {Name: "comment", Value: "hello there"},
        {
            Name:        "file",
            Filename:    "note.txt",
            Content:     []byte("file body bytes"),
            ContentType: "text/plain",
        },
    }
    body, contentType, err := httpcloak.BuildMultipart(fields)
    if err != nil {
        panic(err)
    }

    req := &httpcloak.Request{
        Method: "POST",
        URL:    "https://httpbin.org/post",
        Headers: map[string][]string{
            "Content-Type": {contentType},
        },
        Body: bytes.NewReader(body),
    }
    resp, _ := s.Do(context.Background(), req)
    defer resp.Close()

    body2, _ := resp.Text()
    fmt.Println(body2)
    // form: { "comment": "hello there" }
    // files: { "file": "file body bytes" }
}
```

`BuildMultipart` handles boundary generation, MIME headers per part, and the trailing `--`. The Content-Type it returns is the full string with the boundary parameter, drop it straight into your headers.

For large file uploads, building the whole body in memory is a bad idea. Use `mime/multipart.Writer` directly with `io.Pipe` and stream into the request via `Body: pipeReader`.

</TabItem>
<TabItem value="python" label="Python">

```python
r = s.post(
    "https://httpbin.org/post",
    data={"comment": "hello there"},
    files={"file": ("note.txt", b"file body bytes", "text/plain")},
)

result = r.json()
print(result["form"])   # {"comment": "hello there"}
print(result["files"])  # {"file": "file body bytes"}
```

The `files=` kwarg accepts:

- `{"name": file_object}`: open file, the binding pulls filename and bytes.
- `{"name": (filename, bytes_or_str)}`: explicit filename, content-type sniffed.
- `{"name": (filename, bytes_or_str, content_type)}`: full control.

</TabItem>
<TabItem value="nodejs" label="Node.js">

```js
const r = await s.post("https://httpbin.org/post", {
  data: { comment: "hello there" },
  files: {
    file: {
      filename: "note.txt",
      content: Buffer.from("file body bytes"),
      contentType: "text/plain",
    },
  },
});

console.log(r.json().form);   // { comment: "hello there" }
console.log(r.json().files);  // { file: "file body bytes" }
```

</TabItem>
<TabItem value="dotnet" label=".NET">

```csharp
using HttpCloak;

using var s = new Session(preset: "chrome-latest");

var fields = new Dictionary<string, string> {
    { "comment", "hello there" }
};
var files = new Dictionary<string, MultipartFile> {
    { "file", new MultipartFile {
        Filename = "note.txt",
        Content = System.Text.Encoding.UTF8.GetBytes("file body bytes"),
        ContentType = "text/plain"
    }}
};

var r = s.PostMultipart("https://httpbin.org/post", fields: fields, files: files);
Console.WriteLine(r.Text);
```

</TabItem>
</Tabs>

## Order of fields

Multipart preserves the order you list fields. Most servers parse into a map and don't care, but a few do:

- A server rejects an upload if the file part comes before a CSRF token field.
- A server expects a specific field order baked into its form validation.

When values look fine but you're seeing "field missing" or "invalid form" errors, try reordering. Drop the file last, the token first. The browser-emitted order follows whatever the `<form>` markup says, usually text fields then file inputs.

## Boundary string

Browsers use boundaries like `----WebKitFormBoundaryAbCdEf123456`. Go's stdlib (and therefore httpcloak's `BuildMultipart`) generates random boundaries that look different. For 99% of servers this doesn't matter since the boundary is just a delimiter. A strict WAF that pattern-matches on browser-style boundaries can be handled by building the body yourself with `mime/multipart.Writer.SetBoundary()` and passing a Chrome-shaped one. Rare scenario.

## Verify with httpbin

`POST https://httpbin.org/post` is the one-stop shop for verifying multipart serialization. The response gives you:

- `form`: text fields
- `files`: file fields, with the file content inlined as a string
- `headers["Content-Type"]`: the Content-Type you sent (verify the boundary)

An empty `files` when you sent a file means your boundary or part headers are off. Garbage-looking file content means the encoding is wrong.
