use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::config::parse_duration;
use crate::message::Message;
use crate::registry::Source;

/// Polls one or more JSON HTTP endpoints.
#[derive(Deserialize)]
struct HttpApiConfig {
    urls: Vec<String>,
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
    #[serde(default)]
    headers: std::collections::BTreeMap<String, String>,
    #[serde(default)]
    insecure_skip_verify: bool,
    #[serde(default = "super::default_timeout")]
    timeout: String,
}

struct HttpApiSource {
    cfg: HttpApiConfig,
    client: reqwest::Client,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Source>> {
    let cfg: HttpApiConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.urls.is_empty() {
        bail!("config field \"urls\" is required");
    }
    let client = super::http_client(parse_duration(&cfg.timeout)?, cfg.insecure_skip_verify)?;
    Ok(Box::new(HttpApiSource { cfg, client }))
}

#[async_trait]
impl Source for HttpApiSource {
    async fn collect(&self) -> Result<Vec<Message>> {
        let mut batch = Vec::new();
        for url in &self.cfg.urls {
            let mut req = self.client.get(url).header("Accept", "application/json");
            for (k, v) in &self.cfg.headers {
                req = req.header(k, v);
            }
            if !self.cfg.username.is_empty() {
                req = req.basic_auth(&self.cfg.username, Some(&self.cfg.password));
            }

            let res = req
                .send()
                .await
                .with_context(|| format!("request for {url} failed"))?;
            let status = res.status();
            let body = res
                .bytes()
                .await
                .with_context(|| format!("failed to read response from {url}"))?;
            if !status.is_success() {
                bail!("request for {url} failed: status {status}");
            }

            for mut msg in body_to_messages(&body) {
                msg.meta.insert("url".to_string(), url.clone());
                batch.push(msg);
            }
        }
        Ok(batch)
    }
}

/// Converts a response body into messages: a JSON array yields one message
/// per element, a JSON object yields a single message, and anything else is
/// wrapped as {"raw": "..."}.
fn body_to_messages(body: &[u8]) -> Vec<Message> {
    match serde_json::from_slice::<serde_json::Value>(body) {
        Ok(serde_json::Value::Array(items)) => items
            .into_iter()
            .map(|item| match item {
                obj @ serde_json::Value::Object(_) => Message::new(obj),
                other => Message::new(serde_json::json!({ "value": other })),
            })
            .collect(),
        Ok(obj @ serde_json::Value::Object(_)) => vec![Message::new(obj)],
        Ok(other) => vec![Message::new(serde_json::json!({ "value": other }))],
        Err(_) => {
            let raw = String::from_utf8_lossy(body).trim().to_string();
            vec![Message::new(serde_json::json!({ "raw": raw }))]
        }
    }
}
