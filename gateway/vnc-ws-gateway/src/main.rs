use std::net::SocketAddr;
use std::sync::{Arc, Mutex};

use anyhow::Context;
use futures_util::{SinkExt, StreamExt};
use iroh::endpoint::presets;
use iroh::endpoint::{RecvStream, SendStream};
use iroh::{Endpoint, EndpointId};
use tokio::net::{TcpListener, TcpStream};
use tokio_tungstenite::WebSocketStream;
use tokio_tungstenite::accept_hdr_async;
use tokio_tungstenite::tungstenite::handshake::server::{ErrorResponse, Request, Response};
use tokio_tungstenite::tungstenite::{Bytes, Message, http};

const ALPN: &[u8] = b"daedal-vnc/1";

fn parse_args() -> anyhow::Result<String> {
    let mut listen = String::from("127.0.0.1:8088");
    let mut argv = std::env::args().skip(1);
    while let Some(flag) = argv.next() {
        match flag.as_str() {
            "--listen" => {
                listen = argv.next().context("--listen requires an address")?;
            }
            other => anyhow::bail!("unknown flag: {other}"),
        }
    }
    Ok(listen)
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let listen = parse_args()?;
    let endpoint = Endpoint::bind(presets::N0)
        .await
        .context("failed to bind iroh client endpoint")?;

    println!("vnc-ws-gateway iroh client id: {}", endpoint.id());
    println!("vnc-ws-gateway alpn: {}", String::from_utf8_lossy(ALPN));

    let listener = TcpListener::bind(&listen)
        .await
        .with_context(|| format!("failed to bind websocket listener on {listen}"))?;

    println!("vnc-ws-gateway listening on ws://{listen}/vnc?node=<iroh-node-id>");

    loop {
        let (stream, peer) = listener.accept().await?;
        let endpoint = endpoint.clone();
        tokio::spawn(async move {
            if let Err(err) = handle_connection(endpoint, stream, peer).await {
                eprintln!("vnc-ws-gateway: connection error: {err:#}");
            }
        });
    }
}

fn parse_vnc_request(path: &str, query: Option<&str>) -> Result<EndpointId, String> {
    if path != "/vnc" {
        return Err(format!("unexpected path {path}, expected /vnc"));
    }
    let raw = query
        .into_iter()
        .flat_map(|q| q.split('&'))
        .filter_map(|pair| pair.split_once('='))
        .find(|(key, _)| *key == "node")
        .map(|(_, value)| value)
        .ok_or_else(|| String::from("missing node query parameter"))?;
    raw.parse::<EndpointId>()
        .map_err(|err| format!("invalid node id {raw}: {err}"))
}

fn bad_request(reason: String) -> ErrorResponse {
    http::Response::builder()
        .status(http::StatusCode::BAD_REQUEST)
        .body(Some(reason))
        .expect("static response construction cannot fail")
}

async fn handle_connection(
    endpoint: Endpoint,
    stream: TcpStream,
    peer: SocketAddr,
) -> anyhow::Result<()> {
    let requested: Arc<Mutex<Option<EndpointId>>> = Arc::new(Mutex::new(None));
    let captured = requested.clone();
    let callback = move |req: &Request, response: Response| {
        match parse_vnc_request(req.uri().path(), req.uri().query()) {
            Ok(node_id) => {
                *captured.lock().expect("callback lock poisoned") = Some(node_id);
                Ok(response)
            }
            Err(reason) => Err(bad_request(reason)),
        }
    };

    let ws_stream = accept_hdr_async(stream, callback)
        .await
        .context("websocket handshake failed")?;

    let node_id = requested
        .lock()
        .expect("callback lock poisoned")
        .take()
        .context("handshake accepted without a node id")?;

    println!("vnc-ws-gateway: {peer} dialing {node_id}");
    let connection = endpoint
        .connect(node_id, ALPN)
        .await
        .with_context(|| format!("iroh dial to {node_id} failed"))?;
    let (mut send, recv) = connection
        .open_bi()
        .await
        .context("failed to open bi stream")?;
    send.write_all(&[0])
        .await
        .context("failed to send stream-open preamble")?;

    println!("vnc-ws-gateway: {peer} relaying to {node_id}");
    let result = bridge(ws_stream, send, recv).await;
    println!("vnc-ws-gateway: {peer} relay to {node_id} closed");
    result
}

async fn bridge(
    ws_stream: WebSocketStream<TcpStream>,
    mut send: SendStream,
    mut recv: RecvStream,
) -> anyhow::Result<()> {
    let (mut ws_sink, mut ws_source) = ws_stream.split();

    let ws_to_iroh = async {
        while let Some(message) = ws_source.next().await {
            match message.context("websocket read failed")? {
                Message::Binary(data) => send
                    .write_all(&data)
                    .await
                    .context("iroh write failed")?,
                Message::Text(text) => send
                    .write_all(text.as_bytes())
                    .await
                    .context("iroh write failed")?,
                Message::Close(_) => break,
                Message::Ping(_) | Message::Pong(_) | Message::Frame(_) => {}
            }
        }
        anyhow::Ok(())
    };

    let iroh_to_ws = async {
        let mut buf = vec![0u8; 16 * 1024];
        loop {
            let read = recv.read(&mut buf).await.context("iroh read failed")?;
            let Some(n) = read else {
                break;
            };
            ws_sink
                .send(Message::Binary(Bytes::copy_from_slice(&buf[..n])))
                .await
                .context("websocket write failed")?;
        }
        ws_sink.close().await.ok();
        anyhow::Ok(())
    };

    tokio::select! {
        result = ws_to_iroh => result,
        result = iroh_to_ws => result,
    }
}
