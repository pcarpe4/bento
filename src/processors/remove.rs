use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::message::Message;
use crate::registry::Processor;

/// Removes the given paths from every message.
#[derive(Deserialize)]
struct RemoveConfig {
    paths: Vec<String>,
}

struct RemoveProcessor {
    cfg: RemoveConfig,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Processor>> {
    let cfg: RemoveConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.paths.is_empty() {
        bail!("config field \"paths\" is required");
    }
    Ok(Box::new(RemoveProcessor { cfg }))
}

#[async_trait]
impl Processor for RemoveProcessor {
    async fn process(&self, batch: Vec<Message>) -> Result<Vec<Message>> {
        Ok(batch
            .into_iter()
            .map(|mut msg| {
                for path in &self.cfg.paths {
                    super::remove_path(&mut msg.data, path);
                }
                msg
            })
            .collect())
    }
}
