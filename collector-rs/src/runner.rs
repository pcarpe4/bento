use std::sync::Arc;
use std::time::{Duration, Instant};

use anyhow::{Context, Result};
use log::{error, info};
use tokio::sync::watch;

use crate::config::{parse_duration, Config};
use crate::registry::{Destination, Registry, Source};

struct BoundSource {
    name: String,
    type_name: String,
    interval: Duration,
    source: Box<dyn Source>,
    destinations: Vec<(String, Arc<dyn Destination>)>,
}

/// Owns all configured sources and destinations and schedules collection.
pub struct Runner {
    sources: Vec<BoundSource>,
    destinations: Vec<(String, Arc<dyn Destination>)>,
}

impl Runner {
    /// Instantiates every configured destination and source. A source with
    /// no explicit `destinations` list sends to all destinations.
    pub fn new(conf: &Config, registry: &Registry) -> Result<Self> {
        let mut destinations: Vec<(String, Arc<dyn Destination>)> = Vec::new();
        for d in &conf.destinations {
            let dest = registry
                .new_destination(&d.type_name, &d.config)
                .with_context(|| format!("destination {:?}", d.name))?;
            destinations.push((d.name.clone(), Arc::from(dest)));
        }

        let mut sources = Vec::new();
        for s in &conf.sources {
            let source = registry
                .new_source(&s.type_name, &s.config)
                .with_context(|| format!("source {:?}", s.name))?;
            let interval_str = s.interval.as_deref().unwrap_or(&conf.default_interval);
            let interval = parse_duration(interval_str)?;

            let bound_dests = if s.destinations.is_empty() {
                destinations.clone()
            } else {
                destinations
                    .iter()
                    .filter(|(name, _)| s.destinations.contains(name))
                    .cloned()
                    .collect()
            };

            sources.push(BoundSource {
                name: s.name.clone(),
                type_name: s.type_name.clone(),
                interval,
                source,
                destinations: bound_dests,
            });
        }

        Ok(Self {
            sources,
            destinations,
        })
    }

    /// Collects from every source on its interval until `shutdown` fires.
    /// Sources with a 0 interval collect exactly once.
    pub async fn run(self, shutdown: watch::Receiver<bool>) {
        let mut handles = Vec::new();
        for src in self.sources {
            let mut shutdown = shutdown.clone();
            handles.push(tokio::spawn(async move {
                run_source(src, &mut shutdown).await;
            }));
        }
        for handle in handles {
            let _ = handle.await;
        }
        for (name, dest) in &self.destinations {
            if let Err(err) = dest.close().await {
                error!("failed to close destination {name:?}: {err:#}");
            }
        }
    }
}

async fn run_source(src: BoundSource, shutdown: &mut watch::Receiver<bool>) {
    loop {
        let start = Instant::now();
        match src.source.collect().await {
            Ok(mut batch) => {
                info!(
                    "source {:?} collected {} messages in {:?}",
                    src.name,
                    batch.len(),
                    start.elapsed()
                );
                for msg in &mut batch {
                    msg.meta.insert("source".to_string(), src.name.clone());
                    msg.meta
                        .insert("source_type".to_string(), src.type_name.clone());
                }
                if !batch.is_empty() {
                    for (dest_name, dest) in &src.destinations {
                        if let Err(err) = dest.write(&batch).await {
                            error!(
                                "source {:?} write to destination {dest_name:?} failed: {err:#}",
                                src.name
                            );
                        }
                    }
                }
            }
            Err(err) => error!("source {:?} collection failed: {err:#}", src.name),
        }

        if src.interval.is_zero() {
            info!("source {:?} finished after one collection", src.name);
            return;
        }
        tokio::select! {
            _ = tokio::time::sleep(src.interval) => {}
            _ = shutdown.changed() => return,
        }
    }
}
