use anyhow::{Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::message::Message;
use crate::registry::Input;

fn default_mapping() -> serde_json::Value {
    serde_json::json!({})
}

fn default_count() -> usize {
    1
}

/// Emits a static JSON document on every poll — useful for testing pipelines
/// and as a heartbeat.
#[derive(Deserialize)]
struct GenerateConfig {
    #[serde(default = "default_mapping")]
    json: serde_json::Value,
    #[serde(default = "default_count")]
    count: usize,
}

struct GenerateInput {
    cfg: GenerateConfig,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Input>> {
    let cfg: GenerateConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    Ok(Box::new(GenerateInput { cfg }))
}

#[async_trait]
impl Input for GenerateInput {
    async fn poll(&self) -> Result<Vec<Message>> {
        Ok((0..self.cfg.count)
            .map(|_| Message::new(self.cfg.json.clone()))
            .collect())
    }
}
