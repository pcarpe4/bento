use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::message::Message;
use crate::registry::Processor;

/// Sets a field on every message. The value may reference metadata with
/// "${meta:key}".
#[derive(Deserialize)]
struct SetConfig {
    path: String,
    value: serde_json::Value,
}

struct SetProcessor {
    cfg: SetConfig,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Processor>> {
    let cfg: SetConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.path.is_empty() {
        bail!("config field \"path\" is required");
    }
    Ok(Box::new(SetProcessor { cfg }))
}

fn resolve(value: &serde_json::Value, msg: &Message) -> serde_json::Value {
    if let Some(s) = value.as_str() {
        if let Some(key) = s.strip_prefix("${meta:").and_then(|s| s.strip_suffix('}')) {
            return serde_json::Value::String(msg.meta.get(key).cloned().unwrap_or_default());
        }
    }
    value.clone()
}

#[async_trait]
impl Processor for SetProcessor {
    async fn process(&self, batch: Vec<Message>) -> Result<Vec<Message>> {
        Ok(batch
            .into_iter()
            .map(|mut msg| {
                let value = resolve(&self.cfg.value, &msg);
                super::set_path(&mut msg.data, &self.cfg.path, value);
                msg
            })
            .collect())
    }
}
