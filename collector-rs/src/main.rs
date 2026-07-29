//! Barebones infrastructure data collector: polls configured sources on an
//! interval and writes the results to configured destinations, all defined
//! in a single YAML file.

mod config;
#[cfg(test)]
mod config_test;
mod destinations;
mod message;
mod registry;
mod runner;
mod sources;

use log::{error, info};
use tokio::sync::watch;

fn usage() -> ! {
    eprintln!("usage: collector [-config <path>] [-list]");
    std::process::exit(2);
}

#[tokio::main]
async fn main() {
    env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("info")).init();

    let mut config_path = "config.yaml".to_string();
    let mut list_types = false;
    let mut args = std::env::args().skip(1);
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "-config" | "--config" => config_path = args.next().unwrap_or_else(|| usage()),
            "-list" | "--list" => list_types = true,
            _ => usage(),
        }
    }

    let registry = registry::Registry::builtin();
    if list_types {
        println!("sources: {:?}", registry.source_types());
        println!("destinations: {:?}", registry.destination_types());
        return;
    }

    let conf = match config::load(&config_path) {
        Ok(conf) => conf,
        Err(err) => {
            error!("invalid configuration: {err:#}");
            std::process::exit(1);
        }
    };

    let runner = match runner::Runner::new(&conf, &registry) {
        Ok(runner) => runner,
        Err(err) => {
            error!("failed to initialise: {err:#}");
            std::process::exit(1);
        }
    };

    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    tokio::spawn(async move {
        wait_for_signal().await;
        info!("shutdown signal received");
        let _ = shutdown_tx.send(true);
    });

    info!(
        "collector started with {} sources and {} destinations",
        conf.sources.len(),
        conf.destinations.len()
    );
    runner.run(shutdown_rx).await;
    info!("collector stopped");
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
