use std::fmt::Write as _;
use std::path::{Path, PathBuf};

use anyhow::Context;
use iroh::SecretKey;
use iroh_live::Live;
use iroh_live::media::codec::VideoCodec;
use iroh_live::media::format::VideoPreset;
use iroh_live::media::publish::LocalBroadcast;
use iroh_live::ticket::LiveTicket;
use rusty_capture::{ScreenConfig, X11ScreenCapturer};

struct Args {
    keyfile: PathBuf,
    name: String,
    display: Option<String>,
}

fn parse_args() -> anyhow::Result<Args> {
    let mut keyfile = PathBuf::from("./desktop-broadcast.key");
    let mut name = String::from("desktop");
    let mut display = None;
    let mut argv = std::env::args().skip(1);
    while let Some(flag) = argv.next() {
        match flag.as_str() {
            "--keyfile" => {
                keyfile = PathBuf::from(argv.next().context("--keyfile requires a path")?);
            }
            "--name" => {
                name = argv.next().context("--name requires a broadcast name")?;
            }
            "--display" => {
                display = Some(argv.next().context("--display requires a value")?);
            }
            other => anyhow::bail!("unknown flag: {other}"),
        }
    }
    Ok(Args {
        keyfile,
        name,
        display,
    })
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
            std::fs::write(path, format!("{}\n", encode_hex_key(&key)))
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
    tracing_subscriber::fmt::init();
    let args = parse_args()?;

    if let Some(display) = &args.display {
        unsafe {
            std::env::set_var("DISPLAY", display);
        }
    }

    let secret_key = load_or_create_secret_key(&args.keyfile)?;
    unsafe {
        std::env::set_var("IROH_SECRET", encode_hex_key(&secret_key));
    }

    let live = Live::from_env()
        .await
        .map_err(|err| anyhow::anyhow!("failed to bind iroh endpoint: {err:#}"))?
        .with_router()
        .spawn();

    let broadcast = LocalBroadcast::new();
    let capturer = X11ScreenCapturer::open_default(&ScreenConfig::default()).context(
        "failed to open X11 screen capturer (is DISPLAY set and is an X server reachable?)",
    )?;
    broadcast
        .video()
        .set_source(capturer, VideoCodec::H264, [VideoPreset::P720])
        .context("failed to set video source")?;

    live.publish(args.name.clone(), &broadcast)
        .await
        .map_err(|err| anyhow::anyhow!("failed to register broadcast: {err:#}"))?;

    let ticket = LiveTicket::new(live.endpoint().addr(), &args.name);
    println!("desktop-broadcast node id: {}", live.endpoint().id());
    println!("desktop-broadcast name: {}", args.name);
    println!("desktop-broadcast ticket: {ticket}");

    tokio::signal::ctrl_c()
        .await
        .context("failed to wait for ctrl-c")?;
    live.shutdown().await;
    Ok(())
}
