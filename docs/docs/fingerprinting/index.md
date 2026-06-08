---
title: Fingerprinting
sidebar_position: 1
---

# Fingerprinting

A preset packages every wire-level signal a browser emits into one named bundle: TLS ClientHello, HTTP/2 SETTINGS, header order, priority table. Pick one by name and httpcloak handles the rest. When the defaults don't fit, you can describe the preset as JSON and edit any field, override a single H2 SETTINGS value with the akamai shorthand, or feed in a raw JA3 string for the TLS layer alone.

## In this section

- [What is TLS Fingerprinting](./what-is-tls-fingerprinting): a fast primer on JA3, JA4, akamai H2 hashes
- [Presets](./presets): the full preset list and how to pick the right one
- [JSON Preset Builder](./json-preset-builder): describe_preset, mutate JSON, load_preset_from_json. The customization path most projects end up on
- [Custom JA3](./custom-ja3): WithCustomFingerprint with a raw JA3 string
- [Akamai Shorthand](./akamai-shorthand): H2 shorthand format, change one knob without rebuilding the whole preset
- [Per-Resource Priority](./per-resource-priority): RFC 7540 weights and RFC 9218 priority headers from Sec-Fetch-Dest
