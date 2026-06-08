---
title: Debug With Wireshark
sidebar_position: 4
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Debug With Wireshark

Wireshark plus a TLS keylog gives a packet-level view of what httpcloak is putting on the wire. It is the lowest-level view available short of stepping through the library with a debugger, and it is the right tool when a fingerprint mismatch needs ground truth.

:::info
For fingerprinting issues, Wireshark plus keylog is the source of truth. tls.peet.ws is convenient, but it is a server-side reconstruction, not the wire bytes. The two usually agree. When they don't, Wireshark is right.
:::

## What you'll see

A decrypted capture exposes:

- The full TLS ClientHello, including extension order, GREASE values, and key shares.
- Every HTTP/2 frame: SETTINGS, WINDOW_UPDATE, HEADERS, PRIORITY, PRIORITY_UPDATE.
- HPACK-decoded headers, with Wireshark handling the HPACK decode.
- Every QUIC frame for HTTP/3, including PRIORITY_UPDATE and STREAM frames.
- Server response framing, server settings, and connection-level windowing.

For HTTP/2 fingerprint checks specifically: SETTINGS values, settings order, the first PRIORITY frame on a request stream, and header order in HEADERS frames, all in plain bytes.

## Step 1: Dump TLS keys from your code

`WithKeyLogFile` writes secrets in the SSLKEYLOGFILE format Wireshark expects.

<Tabs groupId="lang">
<TabItem value="go" label="Go">

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/sardanioss/httpcloak"
)

func main() {
    keyLogPath := "/tmp/sslkeys.log"
    // Make sure the file is fresh, old keys won't decrypt new traffic.
    os.Remove(keyLogPath)

    s := httpcloak.NewSession("chrome-latest",
        httpcloak.WithKeyLogFile(keyLogPath),
        httpcloak.WithSessionTimeout(30*time.Second),
    )
    defer s.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    r, err := s.Get(ctx, "https://tls.peet.ws/api/all")
    if err != nil {
        fmt.Println("err:", err); os.Exit(1)
    }
    defer r.Close()
    fmt.Println("status:", r.StatusCode, "proto:", r.Protocol)
    fmt.Println("keys written to:", keyLogPath)
}
```

</TabItem>
<TabItem value="python" label="Python">

```python
import os
import httpcloak

keylog = "/tmp/sslkeys.log"
if os.path.exists(keylog):
    os.remove(keylog)

with httpcloak.Session("chrome-latest", key_log_file=keylog, timeout=30) as s:
    r = s.get("https://tls.peet.ws/api/all")
    print(f"status={r.status_code} proto={r.protocol}")
    print(f"keys written to {keylog}")
```

</TabItem>
</Tabs>

`WithKeyLogFile` overrides the global `SSLKEYLOGFILE` env var for the session it is set on. To enable keylogging for every session in the process, export the env var before running:

```bash
export SSLKEYLOGFILE=/tmp/sslkeys.log
go run main.go
```

httpcloak picks it up at startup with no further configuration.

## Step 2: Verify the keylog file

After the program runs, the file should look like this. One line per secret, in NSS keylog format:

```
CLIENT_HANDSHAKE_TRAFFIC_SECRET <client_random> <secret>
SERVER_HANDSHAKE_TRAFFIC_SECRET <client_random> <secret>
CLIENT_TRAFFIC_SECRET_0 <client_random> <secret>
SERVER_TRAFFIC_SECRET_0 <client_random> <secret>
```

Four lines per TLS 1.3 connection. Fewer than four means the handshake didn't reach the application data stage. Zero lines means either the file path is wrong or the session never opened a TLS connection.

A quick sanity check:

```bash
$ wc -l /tmp/sslkeys.log
4 /tmp/sslkeys.log

$ awk '{print $1}' /tmp/sslkeys.log | sort -u
CLIENT_HANDSHAKE_TRAFFIC_SECRET
CLIENT_TRAFFIC_SECRET_0
SERVER_HANDSHAKE_TRAFFIC_SECRET
SERVER_TRAFFIC_SECRET_0
```

That's a healthy single-connection keylog.

For QUIC and HTTP/3 the same four labels show up, plus `EARLY_TRAFFIC_SECRET` when 0-RTT fires. Recent Wireshark versions read QUIC and TLS keys from the same keylog file.

## Step 3: Capture traffic

Start Wireshark before running the program. A capture started after the ClientHello cannot decrypt the handshake, since Wireshark needs the handshake bytes to map keys to the connection.

Useful capture filters:

| Filter | What it captures |
|--------|------------------|
| `tcp port 443` | All HTTPS over TCP (H1, H2) |
| `udp port 443` | All QUIC (H3) |
| `tcp port 443 or udp port 443` | Both |
| `host tls.peet.ws` | Just traffic to a specific host |

For headless work, `tshark` is the CLI version:

```bash
sudo tshark -i any -f "tcp port 443 or udp port 443" -w /tmp/capture.pcapng
```

Run the Go or Python program while tshark captures, then Ctrl-C tshark.

## Step 4: Point Wireshark at the keylog

In Wireshark:

1. **Edit** → **Preferences** → **Protocols** → **TLS**.
2. **(Pre)-Master-Secret log filename** → `/tmp/sslkeys.log`.
3. OK.

Wireshark re-decodes the capture in place. Any TLS connection whose `client_random` matches a line in the keylog gets fully decrypted.

QUIC needs no extra configuration. The same TLS keylog setting covers it.

## Step 5: Useful display filters

After decryption, the filters worth keeping handy:

| Filter | Shows |
|--------|-------|
| `tls.handshake.type == 1` | Just ClientHello frames |
| `http2` | All HTTP/2 frames |
| `http2.type == 4` | HTTP/2 SETTINGS frames |
| `http2.type == 2` | HTTP/2 PRIORITY frames |
| `http2.type == 1` | HTTP/2 HEADERS frames |
| `http2.type == 12` | HTTP/2 PRIORITY_UPDATE (RFC 9218) |
| `quic` | All QUIC frames |
| `http3` | All HTTP/3 frames |
| `http3.frame_type == 0xf0700` | H3 PRIORITY_UPDATE |
| `tls.handshake.extension.type` | Group by extension type |

## Things to look for

### TLS ClientHello

Click the ClientHello, expand **Secure Sockets Layer** → **TLS** → **Handshake** → **Extensions**. The expected shape:

- Extension order matching the preset's JA4 / peetprint.
- A GREASE extension at position 0 (Chrome-style presets only).
- `key_share` carrying the same curves as the preset's `key_share_curves` list. Modern Chrome ships GREASE + X25519MLKEM768 + X25519.
- ALPN listing `h2` and `http/1.1` (or `h3` for QUIC).

### HTTP/2 SETTINGS

Filter: `http2.type == 4`. The first SETTINGS frame from the client should match the preset's settings:

- Setting 1 (HEADER_TABLE_SIZE): 65536 for Chrome.
- Setting 2 (ENABLE_PUSH): 0.
- Setting 4 (INITIAL_WINDOW_SIZE): 6291456 for Chrome.
- Setting 6 (MAX_HEADER_LIST_SIZE): 262144 for Chrome.

These should land in the order the preset's `settings_order` specifies. Wireshark shows them sequentially, so the order is visible directly in the tree view.

### HTTP/2 PRIORITY on first request stream

For RFC 7540 priorities (Chrome shape), the HEADERS frame on stream 1 carries a PRIORITY flag (`0x20`). Expand **HyperText Transfer Protocol 2** → **Stream** → **Header** and look for:

- `Stream Dependency`: 0
- `Weight`: 256 (Chrome), or whatever the preset specifies
- `Exclusive Bit`: set

If `stream_priority_mode` is `chrome` and this PRIORITY isn't showing up on the first request, something is wrong upstream of the wire.

### HTTP/3 PRIORITY_UPDATE

For HTTP/3, look for QUIC stream frames carrying H3 PRIORITY_UPDATE (frame type 0xf0700). They should reference the request stream ID, not stream 0. An earlier version of the library had a regression where the request stream wasn't referenced correctly, and Wireshark is the most direct way to confirm the installed version behaves.

### HPACK / QPACK header order

Click the HEADERS or QPACK encoder stream. Wireshark decodes the headers into a list. The order should match the preset's `hpack_header_order` (H2) or QPACK ordering (H3). Pseudo-headers come first, in this order for Chrome:

```
:method  :authority  :scheme  :path
```

Other shapes show up too. Safari, for instance, puts `:scheme` before `:authority`.

## tshark for CI

For tests that assert these properties without eyeballing, tshark exposes the same data as fields:

```bash
# Extract just the SETTINGS frames as JSON
tshark -r capture.pcapng -Y "http2.type == 4" \
  -T fields -e http2.settings.identifier -e http2.settings.value

# Print all extension types in the first ClientHello
tshark -r capture.pcapng -Y "tls.handshake.type == 1" \
  -T fields -e tls.handshake.extension.type
```

Wire that into a test harness, capture a known-good run as the baseline, and compare extension types and order on every CI run.

## Common gotchas

**Old keylog file.** Reusing a keylog from a previous run won't decrypt new connections, because each connection has a different `client_random`. Start with a fresh file, or delete before each run.

**Capture started after the handshake.** A capture that begins mid-connection misses the ClientHello. The keys are still valid, but Wireshark needs the handshake bytes to tie keys to the connection.

**HTTP/3 falling back to TCP.** When the H3 dial fails and the library falls back to H2, the trace shows TCP instead of UDP. Not a bug. The `Protocol` field on the response says which transport handled the request: `h3` means UDP, `h2` means TCP.

**Network namespace mismatch.** Code running inside a container or network namespace while tshark runs on the host won't show traffic. Run tshark inside the same namespace as the client.

## Related

- [Akamai Shorthand](../fingerprinting/akamai-shorthand), what the SETTINGS
  values mean
- [Per-resource Priority](../fingerprinting/per-resource-priority), what
  PRIORITY frames are for
- [What is TLS Fingerprinting](../fingerprinting/what-is-tls-fingerprinting)
 , the ClientHello you're inspecting
