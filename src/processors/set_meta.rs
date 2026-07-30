use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::message::Message;
use crate::registry::Processor;

/// Sets a metadata key on every message. The value may reference a data
/// field with "${data:path.to.field}".
#[derive(Deserialize)]
struct SetMetaConfig {
    key: String,
    value: String,
}

struct SetMetaProcessor {
    cfg: SetMetaConfig,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Processor>> {
    let cfg: SetMetaConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.key.is_empty() {
        bail!("config field \"key\" is required");
    }
    Ok(Box::new(SetMetaProcessor { cfg }))
}

#[async_trait]
impl Processor for SetMetaProcessor {
    async fn process(&self, batch: Vec<Message>) -> Result<Vec<Message>> {
        Ok(batch
            .into_iter()
            .map(|mut msg| {
                let value = match self
                    .cfg
                    .value
                    .strip_prefix("${data:")
                    .and_then(|s| s.strip_suffix('}'))
                {
                    Some(path) => super::get_path(&msg.data, path)
                        .map(|v| match v {
                            serde_json::Value::String(s) => s.clone(),
                            other => other.to_string(),
                        })
                        .unwrap_or_default(),
                    None => self.cfg.value.clone(),
                };
                msg.meta.insert(self.cfg.key.clone(), value);
                msg
            })
            .collect())
    }
}
