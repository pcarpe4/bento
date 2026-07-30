use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::config::parse_duration;
use crate::message::Message;
use crate::registry::Output;

fn default_timeout() -> String {
    "15s".to_string()
}

/// POSTs each batch as a JSON array to a URL.
#[derive(Deserialize)]
struct HttpPostConfig {
    url: String,
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
    #[serde(default)]
    headers: std::collections::BTreeMap<String, String>,
    #[serde(default)]
    insecure_skip_verify: bool,
    #[serde(default = "default_timeout")]
    timeout: String,
}

struct HttpPostDestination {
    cfg: HttpPostConfig,
    client: reqwest::Client,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Output>> {
    let cfg: HttpPostConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.url.is_empty() {
        bail!("config field \"url\" is required");
    }
    let client = reqwest::Client::builder()
        .timeout(parse_duration(&cfg.timeout)?)
        .danger_accept_invalid_certs(cfg.insecure_skip_verify)
        .build()?;
    Ok(Box::new(HttpPostDestination { cfg, client }))
}

#[async_trait]
impl Output for HttpPostDestination {
    async fn write(&self, batch: &[Message]) -> Result<()> {
        let mut req = self.client.post(&self.cfg.url).json(batch);
        for (k, v) in &self.cfg.headers {
            req = req.header(k, v);
        }
        if !self.cfg.username.is_empty() {
            req = req.basic_auth(&self.cfg.username, Some(&self.cfg.password));
        }

        let res = req
            .send()
            .await
            .with_context(|| format!("post to {} failed", self.cfg.url))?;
        if !res.status().is_success() {
            bail!("post to {} failed: status {}", self.cfg.url, res.status());
        }
        Ok(())
    }
}
