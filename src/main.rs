//! bento-rs: a Rust port of the Bento stream processor. Streams are defined
//! in YAML as input -> pipeline.processors -> output and polled on an
//! interval, with failed deliveries retried with backoff.

mod config;
#[cfg(test)]
mod config_test;
mod engine;
mod inputs;
mod message;
mod outputs;
mod processors;
mod registry;

use log::{error, info};
use tokio::sync::watch;

fn usage() -> ! {
    eprintln!(
        "usage:\n  bento-rs run <config.yaml>     run a single stream\n  bento-rs streams <dir>         run every *.yaml in a directory as a stream\n  bento-rs lint <config.yaml>    validate a config without running it\n  bento-rs list                  print registered component types"
    );
    std::process::exit(2);
}

#[tokio::main]
async fn main() {
    env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("info")).init();

    let args: Vec<String> = std::env::args().skip(1).collect();
    let registry = registry::Registry::builtin();

    match args.first().map(String::as_str) {
        Some("list") => {
            println!("inputs: {:?}", registry.input_types());
            println!("processors: {:?}", registry.processor_types());
            println!("outputs: {:?}", registry.output_types());
        }
        Some("lint") => {
            let path = args.get(1).map(String::as_str).unwrap_or_else(|| usage());
            match config::load_stream(std::path::Path::new(path))
                .and_then(|conf| engine::Stream::build("lint", &conf, &registry))
            {
                Ok(_) => println!("OK"),
                Err(err) => {
                    eprintln!("{err:#}");
                    std::process::exit(1);
                }
            }
        }
        Some("run") => {
            let path = args.get(1).map(String::as_str).unwrap_or_else(|| usage());
            let name = std::path::Path::new(path)
                .file_stem()
                .and_then(|s| s.to_str())
                .unwrap_or("stream")
                .to_string();
            match config::load_stream(std::path::Path::new(path)) {
                Ok(conf) => run(vec![(name, conf)], &registry).await,
                Err(err) => {
                    error!("{err:#}");
                    std::process::exit(1);
                }
            }
        }
        Some("streams") => {
            let dir = args.get(1).map(String::as_str).unwrap_or_else(|| usage());
            match config::load_streams_dir(std::path::Path::new(dir)) {
                Ok(streams) => run(streams, &registry).await,
                Err(err) => {
                    error!("{err:#}");
                    std::process::exit(1);
                }
            }
        }
        _ => usage(),
    }
}

async fn run(configs: Vec<(String, config::StreamConf)>, registry: &registry::Registry) {
    let mut streams = Vec::new();
    for (name, conf) in &configs {
        match engine::Stream::build(name, conf, registry) {
            Ok(stream) => streams.push(stream),
            Err(err) => {
                error!("failed to build stream {name:?}: {err:#}");
                std::process::exit(1);
            }
        }
    }

    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    tokio::spawn(async move {
        wait_for_signal().await;
        info!("shutdown signal received");
        let _ = shutdown_tx.send(true);
    });

    info!("running {} stream(s)", streams.len());
    engine::run_streams(streams, shutdown_rx).await;
    info!("stopped");
}

async fn wait_for_signal() {
    #[cfg(unix)]
    {
        let mut term = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("failed to install SIGTERM handler");
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {}
            _ = term.recv() => {}
        }
    }
    #[cfg(not(unix))]
    {
        let _ = tokio::signal::ctrl_c().await;
    }
}
