# gateway

Cargo workspace tunneling RFB (VNC) traffic between a Firecracker guest's
local VNC server and a browser noVNC client, over an iroh QUIC connection
instead of a raw TCP hop across hosts.

## ALPN

`b"daedal-vnc/1"`, defined once in each binary crate as `ALPN` and passed to
both `Endpoint::builder(..).alpns(vec![ALPN.to_vec()])` (host-agent, server
role) and `ep.connect(addr, ALPN)` (gateway, client role).

## Why no envelope

RFB (the VNC wire protocol) is already a self-framed, ordered byte stream:
after the initial handshake every message carries its own length or is
implicitly delimited by the protocol state machine, exactly like the raw TCP
socket a VNC client normally dials. An iroh bidirectional QUIC stream
(`SendStream`/`RecvStream`) is itself an ordered, reliable byte stream. This
means the tunnel is a byte-for-byte pass-through: no message framing, no
length-prefixing, no protocol translation. Both binaries in this workspace
exist only to bridge byte streams across three transports (TCP, iroh QUIC,
WebSocket) -- none of them need to understand a single byte of RFB.

The one exception is a single stream-open preamble byte (`0x00`) the gateway
writes immediately after `open_bi()` and the agent reads and discards before
relaying. QUIC bi-streams are registered with the peer lazily, only once the
opener sends data, and RFB is a server-talks-first protocol: without the
preamble the agent's `accept_bi()` would never fire (browser waits for the
server greeting, gateway has sent nothing), a mutual deadlock. The preamble is
consumed on the agent side, so the relayed payload remains byte-for-byte RFB.

## Roles

### `vnc-tunnel-agent` (host-agent, runs next to the VNC server)

Binds an iroh `Endpoint` with the `daedal-vnc/1` ALPN registered via
`Endpoint::builder(presets::N0).alpns(..).bind()`, and accepts inbound iroh
connections with `ep.accept().await` (an `Option<Incoming>`, `None` only once
the endpoint itself is closed) followed by awaiting the `Incoming` (which
implements `IntoFuture`, i.e. `incoming.await`) to complete the handshake into
a `Connection`. For each accepted connection it runs `accept_bi()`, reads the
one-byte stream-open preamble, dials the local VNC TCP port (`--vnc-addr`,
default `127.0.0.1:5901`) and hands the resulting `tokio::net::TcpStream` and
the iroh bi-stream (wrapped into one duplex value via
`gateway_relay_common::Duplex::new(recv_stream, send_stream)`) to
`gateway_relay_common::relay`, which copies bytes in both directions
concurrently until either side closes.

The secret key behind the NodeId is persisted via `--keyfile` (default
`./vnc-tunnel-agent.key`, written `0600` as 64 lowercase hex chars): loaded if
the file exists, generated and saved on first run, so the NodeId is stable
across restarts and can be registered once with `daedald`.

Prints its `EndpointId` (`endpoint.id()`) on startup; this is the value an
operator registers with `daedald` so the gateway/frontend can look up which
NodeId corresponds to which host's desktop guest.

Witnessed end-to-end 2026-07-27: two consecutive runs against the same
keyfile printed the identical NodeId, and a 27-byte payload sent by a raw
WebSocket client came back byte-identical through
WS -> iroh -> TCP -> echo server -> TCP -> iroh -> WS.

### `vnc-ws-gateway` (browser-facing bridge)

Runs a plain `tokio::net::TcpListener` speaking WebSocket
(`tokio_tungstenite::accept_hdr_async`) at `ws://127.0.0.1:8088/vnc?node=<id>`
(override with `--listen HOST:PORT`). Any other request path, a missing `node`
query parameter, or an unparseable node id rejects the upgrade with a 400.
`<id>` is the iroh `EndpointId` printed by the target `vnc-tunnel-agent`,
formatted the way `EndpointId`'s `Display` impl actually renders it: a
lowercase-hex-encoded Ed25519 public key (32 bytes -> 64 hex chars) -- NOT
base58 despite that shorthand appearing in earlier planning notes.
`EndpointId::from_str` also accepts the base32 form iroh sometimes prints
elsewhere, so either encoding round-trips, but hex is what
`println!("{}", endpoint.id())` on the agent side actually produces and is
what should be pasted into the query string.

For each accepted WebSocket connection the gateway dials that NodeId over
iroh using a client-only endpoint (`Endpoint::bind(presets::N0)`, no ALPN
server role needed since the gateway never accepts) via
`ep.connect(node_id, ALPN)`, opens a bidirectional stream with
`conn.open_bi()`, writes the one-byte preamble, and relays WebSocket
binary-frame payload bytes against the iroh stream's bytes in both
directions. WebSocket frame boundaries carry no meaning for RFB (a pure
byte-stream consumer on both ends), so the bridge unwraps
`Message::Binary`/`Message::Text` payloads on the way from the browser to
iroh and wraps 16 KiB-max byte chunks as `Message::Binary` on the way back,
via a `futures_util` split-sink/`tokio::select!` loop rather than
`gateway_relay_common::relay` (the WS side is a frame stream, not an
`AsyncRead`/`AsyncWrite` pair).

Multiple concurrent browser tabs against the same node work with no extra
gateway-side fan-out logic, since each gets its own independently dialed
`open_bi()` stream and Xvnc/tigervnc natively supports multiple simultaneous
RFB viewers.

## `gateway-relay-common`

The one piece of this workspace that is fully implemented, not scaffolded:
`relay(a, b)` takes any two values that are both `AsyncRead + AsyncWrite +
Unpin` (a "duplex byte stream"), splits each into a read half and write half
via `tokio::io::split`, and runs `tokio::io::copy` in both directions
concurrently with `tokio::select!` -- the loop exits as soon as either
direction's copy finishes (EOF or error), which is exactly "until either
closes". `Duplex<R, W>` is the adapter that lets a split pair like iroh's
`(RecvStream, SendStream)` be passed to `relay` as a single value satisfying
that bound, since unlike `TcpStream` an iroh bi-stream is handed out as two
already-separate halves rather than one combined type.

## Building

```
cd gateway
cargo build
```

Depends on the real published `iroh = "1.0.3"` crate (not a vendored fork),
`tokio`, `tokio-tungstenite`, and `anyhow`, pinned once in
`[workspace.dependencies]` and inherited by each member crate via `{
workspace = true }`.
