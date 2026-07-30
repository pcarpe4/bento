use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::message::Message;
use crate::registry::Processor;

/// Projects each message down to only the given paths.
#[derive(Deserialize)]
struct SelectConfig {
    paths: Vec<String>,
}

struct SelectProcessor {
    cfg: SelectConfig,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Processor>> {
    let cfg: SelectConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.paths.is_empty() {
        bail!("config field \"paths\" is required");
    }
    Ok(Box::new(SelectProcessor { cfg }))
}

#[async_trait]
impl Processor for SelectProcessor {
    async fn process(&self, batch: Vec<Message>) -> Result<Vec<Message>> {
        Ok(batch
            .into_iter()
            .map(|mut msg| {
                let mut projected = serde_json::json!({});
                for path in &self.cfg.paths {
                    if let Some(found) = super::get_path(&msg.data, path) {
                        super::set_path(&mut projected, path, found.clone());
                    }
                }
                msg.data = projected;
                msg
            })
            .collect())
    }
}
