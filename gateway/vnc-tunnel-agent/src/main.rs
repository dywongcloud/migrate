use std::fmt::Write as _;
use std::path::{Path, PathBuf};

use anyhow::Context;
use gateway_relay_common::{Duplex, relay};
use iroh::endpoint::{Incoming, presets};
use iroh::{Endpoint, SecretKey};
use tokio::net::TcpStream;

const ALPN: &[u8] = b"daedal-vnc/1";

struct Args {
    keyfile: PathBuf,
    vnc_addr: String,
}

fn parse_args() -> anyhow::Result<Args> {
    let mut keyfile = PathBuf::from("./vnc-tunnel-agent.key");
    let mut vnc_addr = String::from("127.0.0.1:5901");
    let mut argv = std::env::args().skip(1);
    while let Some(flag) = argv.next() {
        match flag.as_str() {
            "--keyfile" => {
                keyfile = PathBuf::from(argv.next().context("--keyfile requires a path")?);
            }
            "--vnc-addr" => {
                vnc_addr = argv.next().context("--vnc-addr requires an address")?;
            }
            other => anyhow::bail!("unknown flag: {other}"),
        }
    }
    Ok(Args { keyfile, vnc_addr })
}

fn decode_hex_key(text: &str) -> anyhow::Result<[u8; 32]> {
    anyhow::ensure!(
        text.len() == 64,
        "expected 64 hex chars, got {}",
        text.len()
    );
    let mut bytes = [0u8; 32];
    for (slot, pair) in bytes.iter_mut().zip(text.as_bytes().chunks(2)) {
        let pair = std::str::from_utf8(pair).context("keyfile is not utf-8")?;
        *slot = u8::from_str_radix(pair, 16)
            .with_context(|| format!("invalid hex pair {pair:?}"))?;
    }
    Ok(bytes)
}

fn encode_hex_key(key: &SecretKey) -> String {
    let mut text = String::with_capacity(65);
    for byte in key.to_bytes() {
        write!(text, "{byte:02x}").expect("writing to a String cannot fail");
    }
    text.push('\n');
    text
}

fn load_or_create_secret_key(path: &Path) -> anyhow::Result<SecretKey> {
    match std::fs::read_to_string(path) {
        Ok(text) => {
            let bytes = decode_hex_key(text.trim())
                .with_context(|| format!("invalid secret key in {}", path.display()))?;
            Ok(SecretKey::from_bytes(&bytes))
        }
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {
            let key = SecretKey::generate();
            std::fs::write(path, encode_hex_key(&key))
                .with_context(|| format!("failed to write keyfile {}", path.display()))?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600))
                    .with_context(|| format!("failed to chmod keyfile {}", path.display()))?;
            }
            Ok(key)
        }
        Err(err) => {
            Err(err).with_context(|| format!("failed to read keyfile {}", path.display()))
        }
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let args = parse_args()?;
    let secret_key = load_or_create_secret_key(&args.keyfile)?;
    let endpoint = Endpoint::builder(presets::N0)
        .secret_key(secret_key)
        .alpns(vec![ALPN.to_vec()])
        .bind()
        .await
        .context("failed to bind iroh endpoint")?;

    println!("vnc-tunnel-agent node id: {}", endpoint.id());
    println!("vnc-tunnel-agent alpn: {}", String::from_utf8_lossy(ALPN));
    println!("vnc-tunnel-agent keyfile: {}", args.keyfile.display());
    println!("vnc-tunnel-agent vnc addr: {}", args.vnc_addr);

    loop {
        let Some(incoming) = endpoint.accept().await else {
            break;
        };
        let vnc_addr = args.vnc_addr.clone();
        tokio::spawn(async move {
            if let Err(err) = handle_incoming(incoming, vnc_addr).await {
                eprintln!("vnc-tunnel-agent: connection error: {err:#}");
            }
        });
    }

    Ok(())
}

async fn handle_incoming(incoming: Incoming, vnc_addr: String) -> anyhow::Result<()> {
    let connection = incoming.await.context("failed to complete handshake")?;
    let remote = connection.remote_id();
    let (send, mut recv) = connection
        .accept_bi()
        .await
        .context("failed to accept bi stream")?;
    let mut preamble = [0u8; 1];
    recv.read_exact(&mut preamble)
        .await
        .context("failed to read stream-open preamble")?;
    let vnc = TcpStream::connect(&vnc_addr)
        .await
        .with_context(|| format!("failed to dial vnc server at {vnc_addr}"))?;
    println!("vnc-tunnel-agent: relaying {remote} <-> {vnc_addr}");
    relay(Duplex::new(recv, send), vnc)
        .await
        .context("relay ended with error")?;
    println!("vnc-tunnel-agent: relay for {remote} closed");
    Ok(())
}
