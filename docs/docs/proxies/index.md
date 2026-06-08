---
title: Proxies
sidebar_position: 1
---

# Proxies

httpcloak speaks HTTP CONNECT, SOCKS5, SOCKS5 with UDP, and MASQUE. Pick whichever one your upstream offers and your protocol mix needs.

## In this section

- [Overview](./overview): when to pick what, what each protocol carries, what it solves.
- [HTTP CONNECT](./http-connect): the classic. Plain HTTPS tunneling.
- [SOCKS5](./socks5): the residential-provider workhorse, with auth.
- [SOCKS5 UDP](./socks5-udp): UDP ASSOCIATE for pushing HTTP/3 through SOCKS5.
- [MASQUE](./masque): HTTP/3 CONNECT-UDP. QUIC inside QUIC.
- [Source Address Binding](./source-address-binding): pin every dial to a local IP you pick.
