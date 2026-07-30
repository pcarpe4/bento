use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::message::Message;
use crate::registry::Processor;

/// Keeps only messages where `path` exists and (when `equals` is set)
/// matches the given value.
#[derive(Deserialize)]
struct FilterConfig {
    path: String,
    #[serde(default)]
    equals: Option<serde_json::Value>,
}

struct FilterProcessor {
    cfg: FilterConfig,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Processor>> {
    let cfg: FilterConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.path.is_empty() {
        bail!("config field \"path\" is required");
    }
    Ok(Box::new(FilterProcessor { cfg }))
}

#[async_trait]
impl Processor for FilterProcessor {
    async fn process(&self, batch: Vec<Message>) -> Result<Vec<Message>> {
        Ok(batch
            .into_iter()
            .filter(|msg| match super::get_path(&msg.data, &self.cfg.path) {
                Some(found) => match &self.cfg.equals {
                    Some(expected) => found == expected,
                    None => true,
                },
                None => false,
            })
            .collect())
    }
}
