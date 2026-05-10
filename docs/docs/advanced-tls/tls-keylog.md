---
title: TLS Keylog
sidebar_position: 4
---

# TLS Keylog

A TLS keylog is a text file containing the per-connection secrets needed to decrypt a captured TLS session. Wireshark, Chrome, curl, and httpcloak all share the same `SSLKEYLOGFILE` format: ASCII, one secret per line, keyed by the ClientRandom of each handshake. Point Wireshark at the file and any TLS stream in the capture whose ClientRandom matches a line gets decrypted live in the UI.

`WithKeyLogFile(path)` opens the named file in append mode and writes every TLS handshake's per-connection secrets into it. Each new handshake adds a few lines. The format is simple, one secret per line.

## Format

Each line is space-separated:

```
<label> <client_random_hex> <secret_hex>
```

The label tells Wireshark which secret this is. For TLS 1.3 each connection emits four lines:

```
CLIENT_HANDSHAKE_TRAFFIC_SECRET <client_random> <secret>
SERVER_HANDSHAKE_TRAFFIC_SECRET <client_random> <secret>
CLIENT_TRAFFIC_SECRET_0         <client_random> <secret>
SERVER_TRAFFIC_SECRET_0         <client_random> <secret>
```

For TLS 1.2 connections the format collapses to a single line:

```
CLIENT_RANDOM <client_random> <master_secret>
```

Wireshark only needs the four 1.3 traffic secrets to decrypt the application data; the lib's uTLS fork doesn't emit `EXPORTER_SECRET` or any 0-RTT-specific labels, and Wireshark doesn't ask for them when reading the keylog.

`<client_random>` is 64 hex chars (32 bytes). `<secret>` is 64 or 96 hex chars depending on the cipher suite. Wireshark matches lines to captured connections by the ClientRandom field.

## Setup

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/sardanioss/httpcloak"
)

func main() {
    keylog := "/tmp/httpcloak-keys.txt"
    _ = os.Remove(keylog) // start fresh

    s := httpcloak.NewSession("chrome-latest",
        httpcloak.WithKeyLogFile(keylog),
    )
    defer s.Close()

    resp, err := s.Get(context.Background(), "https://example.com/")
    if err != nil {
        panic(err)
    }
    fmt.Println("status:", resp.StatusCode)

    data, _ := os.ReadFile(keylog)
    fmt.Printf("keylog (%d bytes):\n%s", len(data), string(data))
}
```

A run produces something like:

```
status: 200
keylog (632 bytes):
CLIENT_HANDSHAKE_TRAFFIC_SECRET 2bb1...4f SERVER_HANDSHAKE_TRAFFIC_SECRET ...
CLIENT_TRAFFIC_SECRET_0 ...
SERVER_TRAFFIC_SECRET_0 ...
```

</TabItem>
</Tabs>

(The bindings expose the same keylog feature via the equivalent `key_log_file` / `keyLogFile` option, with an identical workflow. The Go example above is the canonical one for Wireshark debugging.)

## Pointing Wireshark at the file

1. Edit > Preferences > Protocols > TLS.
2. Find the field `(Pre)-Master-Secret log filename`.
3. Browse to the path passed to `WithKeyLogFile`.
4. Click OK.

Wireshark watches the file. New lines appended while a capture is open get picked up live. Start a capture, run a request that writes a new key, then check the TLS stream in Wireshark, and the packet detail pane shows a "Decrypted TLS" tab with plaintext HTTP/2 frames or the H1 request line inside.

For H3 (QUIC), the same keys get written but the Wireshark preference to enable is QUIC TLS Decryption. As of Wireshark 4.x, pointing the TLS keylog setting at the file is enough, QUIC inherits from it automatically.

## Override priority

`WithKeyLogFile` overrides the global `SSLKEYLOGFILE` env var for that specific session. When both are set, the explicit path wins. When only the env var is set, every session writes to it. One session can be keylogging while another stays silent, just by toggling the option per session.

## Per-session vs global

| Setup                                | Behavior                                  |
| :----------------------------------- | :---------------------------------------- |
| `SSLKEYLOGFILE=/tmp/k.log` env var   | Every session writes there                |
| `WithKeyLogFile("/tmp/s1.log")` only | Only that session writes, others silent   |
| Both set                             | The explicit option wins for that session |
| Neither set                          | No keylog                                 |

## Process-wide writer (non-file destinations)

`WithKeyLogFile` and `SSLKEYLOGFILE` both target a path on disk. For destinations that aren't files (a ring buffer, an S3 multipart uploader, a syslog pipe, an in-memory `bytes.Buffer` for tests), the lib exposes a process-wide writer:

```go
import (
    "bytes"
    "github.com/sardanioss/httpcloak"
)

var sink bytes.Buffer
httpcloak.SetKeyLogWriter(&sink)
defer httpcloak.SetKeyLogWriter(nil)   // detach when done

// every session that doesn't have its own WithKeyLogFile now writes to sink
```

`SetKeyLogWriter` accepts any `io.Writer` and applies process-wide. Pass `nil` to detach. A session with an explicit `WithKeyLogFile` still wins for that session; the writer is the fallback for sessions that don't pin a path themselves. Internally this delegates to `transport.SetKeyLogWriter`, with a few sibling helpers worth knowing about for advanced cases (`transport.GetKeyLogWriter`, `transport.SetKeyLogFile(path)`, `transport.NewKeyLogFileWriter(path)`, `transport.CloseKeyLog()`); reach for those when you need explicit control over the file lifecycle.

## When you actually need this

- Verifying ECH fired. Decrypt the inner ClientHello and inspect the `encrypted_client_hello` extension.
- Checking that header order on the wire matches what was set. DevTools won't show raw header order, so byte-level proof has to come from a decrypted capture.
- Debugging a server's H2 frame layout when something doesn't add up. Wireshark's HTTP/2 dissector is good and will show which frame the server sent and in what order.
- Reproducing a Chromium DevTools-style waterfall with raw bytes underneath. Useful for benchmarking and for proving correctness of a custom fingerprint.

## When you don't need this

- Most of the time the question is "the server's response is wrong, why". For that, print the response headers and body. Keylogging is for when the disagreement is below the HTTP layer.
- Production. Don't ship `WithKeyLogFile` enabled into prod. The file holds material that lets anyone with read access decrypt live traffic. If logging to disk is unavoidable, write to a tightly permissioned, rotated path.

## Related recipes

For a step-by-step Wireshark walkthrough including capture filters and TLS decryption setup, see [Debugging with Wireshark](/recipes/debug-with-wireshark).
