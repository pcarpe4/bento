use std::sync::Arc;
use std::time::{Duration, Instant};

use anyhow::{Context, Result};
use log::{error, info, warn};
use tokio::sync::watch;

use crate::config::{interval_from, StreamConf};
use crate::registry::{Input, Output, Processor, Registry};

/// A fully constructed stream: input -> processors -> output, polled on an
/// interval.
pub struct Stream {
    name: String,
    input_type: String,
    interval: Duration,
    input: Box<dyn Input>,
    processors: Vec<Box<dyn Processor>>,
    output: Arc<dyn Output>,
}

impl Stream {
    pub fn build(name: &str, conf: &StreamConf, registry: &Registry) -> Result<Self> {
        let input = registry
            .new_input(&conf.input.type_name, &conf.input.config)
            .with_context(|| format!("stream {name:?} input"))?;
        let interval =
            interval_from(&conf.input.config).with_context(|| format!("stream {name:?} input"))?;

        let mut processors = Vec::new();
        for (i, p) in conf.pipeline.processors.iter().enumerate() {
            processors.push(
                registry
                    .new_processor(&p.type_name, &p.config)
                    .with_context(|| format!("stream {name:?} processor {i}"))?,
            );
        }

        let output = registry
            .new_output(&conf.output.type_name, &conf.output.config)
            .with_context(|| format!("stream {name:?} output"))?;

        Ok(Self {
            name: name.to_string(),
            input_type: conf.input.type_name.clone(),
            interval,
            input,
            processors,
            output: Arc::from(output),
        })
    }

    /// Polls the input on its interval until `shutdown` fires. Delivery is
    /// at-least-once while the process lives: failed writes are retried with
    /// exponential backoff rather than dropped.
    pub async fn run(self, mut shutdown: watch::Receiver<bool>) {
        loop {
            let start = Instant::now();
            match self.input.poll().await {
                Ok(batch) => {
                    info!(
                        "stream {:?} collected {} messages in {:?}",
                        self.name,
                        batch.len(),
                        start.elapsed()
                    );
                    let mut batch = batch;
                    for msg in &mut batch {
                        msg.meta.insert("stream".to_string(), self.name.clone());
                        msg.meta
                            .insert("input_type".to_string(), self.input_type.clone());
                    }

                    match self.apply_processors(batch).await {
                        Ok(batch) if batch.is_empty() => {}
                        Ok(batch) => {
                            if !self.deliver(&batch, &mut shutdown).await {
                                break; // shutdown during delivery
                            }
                        }
                        Err(err) => error!("stream {:?} processor failed: {err:#}", self.name),
                    }
                }
                Err(err) => error!("stream {:?} input poll failed: {err:#}", self.name),
            }

            if self.interval.is_zero() {
                info!("stream {:?} finished after one poll", self.name);
                break;
            }
            tokio::select! {
                _ = tokio::time::sleep(self.interval) => {}
                _ = shutdown.changed() => break,
            }
        }

        if let Err(err) = self.output.close().await {
            error!("stream {:?} failed to close output: {err:#}", self.name);
        }
    }

    async fn apply_processors(
        &self,
        mut batch: Vec<crate::message::Message>,
    ) -> Result<Vec<crate::message::Message>> {
        for processor in &self.processors {
            batch = processor.process(batch).await?;
            if batch.is_empty() {
                break;
            }
        }
        Ok(batch)
    }

    /// Writes with capped exponential backoff until success or shutdown.
    /// Returns false if shutdown interrupted delivery.
    async fn deliver(
        &self,
        batch: &[crate::message::Message],
        shutdown: &mut watch::Receiver<bool>,
    ) -> bool {
        let mut backoff = Duration::from_secs(1);
        loop {
            match self.output.write(batch).await {
                Ok(()) => return true,
                Err(err) => {
                    warn!(
                        "stream {:?} write failed (retrying in {:?}): {err:#}",
                        self.name, backoff
                    );
                    tokio::select! {
                        _ = tokio::time::sleep(backoff) => {}
                        _ = shutdown.changed() => {
                            error!(
                                "stream {:?} shut down with {} undelivered messages",
                                self.name,
                                batch.len()
                            );
                            return false;
                        }
                    }
                    backoff = (backoff * 2).min(Duration::from_secs(60));
                }
            }
        }
    }
}

/// Runs a set of streams concurrently until shutdown.
pub async fn run_streams(streams: Vec<Stream>, shutdown: watch::Receiver<bool>) {
    let mut handles = Vec::new();
    for stream in streams {
        let shutdown = shutdown.clone();
        handles.push(tokio::spawn(stream.run(shutdown)));
    }
    for handle in handles {
        let _ = handle.await;
    }
}
