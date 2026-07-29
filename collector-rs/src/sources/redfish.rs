use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::config::parse_duration;
use crate::message::Message;
use crate::registry::Source;

fn default_endpoints() -> Vec<String> {
    vec!["/redfish/v1/Systems".to_string()]
}

fn default_true() -> bool {
    true
}

/// Collects resources from BMCs (Dell iDRAC, HPE iLO, Lenovo XCC, ...) via
/// the DMTF Redfish API using HTTP basic auth. Collection resources
/// (containing a Members array) are expanded into one message per member.
#[derive(Deserialize)]
struct RedfishConfig {
    urls: Vec<String>,
    username: String,
    #[serde(default)]
    password: String,
    #[serde(default = "default_endpoints")]
    endpoints: Vec<String>,
    #[serde(default = "default_true")]
    expand_collections: bool,
    #[serde(default = "default_true")]
    insecure_skip_verify: bool,
    #[serde(default = "super::default_timeout")]
    timeout: String,
}

struct RedfishSource {
    cfg: RedfishConfig,
    client: reqwest::Client,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Source>> {
    let mut cfg: RedfishConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.urls.is_empty() {
        bail!("config field \"urls\" is required");
    }
    if cfg.username.is_empty() {
        bail!("config field \"username\" is required");
    }
    for url in &mut cfg.urls {
        while url.ends_with('/') {
            url.pop();
        }
    }
    let client = super::http_client(parse_duration(&cfg.timeout)?, cfg.insecure_skip_verify)?;
    Ok(Box::new(RedfishSource { cfg, client }))
}

#[async_trait]
impl Source for RedfishSource {
    async fn collect(&self) -> Result<Vec<Message>> {
        let mut batch = Vec::new();
        for base_url in &self.cfg.urls {
            for endpoint in &self.cfg.endpoints {
                let doc = self.fetch(base_url, endpoint).await?;

                match member_paths(&doc) {
                    Some(paths) if self.cfg.expand_collections => {
                        for path in paths {
                            let member = self.fetch(base_url, &path).await?;
                            batch.push(message(base_url, endpoint, member));
                        }
                    }
                    _ => batch.push(message(base_url, endpoint, doc)),
                }
            }
        }
        Ok(batch)
    }
}

impl RedfishSource {
    async fn fetch(&self, base_url: &str, path: &str) -> Result<serde_json::Value> {
        let url = if path.starts_with('/') {
            format!("{base_url}{path}")
        } else {
            format!("{base_url}/{path}")
        };
        let res = self
            .client
            .get(&url)
            .header("Accept", "application/json")
            .basic_auth(&self.cfg.username, Some(&self.cfg.password))
            .send()
            .await
            .with_context(|| format!("request for {url} failed"))?;
        if !res.status().is_success() {
            bail!("request for {url} failed: status {}", res.status());
        }
        res.json()
            .await
            .with_context(|| format!("failed to parse response for {url}"))
    }
}

/// Returns referenced member paths if doc is a Redfish collection.
fn member_paths(doc: &serde_json::Value) -> Option<Vec<String>> {
    let members = doc.get("Members")?.as_array()?;
    Some(
        members
            .iter()
            .filter_map(|m| m.get("@odata.id")?.as_str().map(str::to_string))
            .collect(),
    )
}

fn message(base_url: &str, endpoint: &str, doc: serde_json::Value) -> Message {
    let odata_id = doc
        .get("@odata.id")
        .and_then(|v| v.as_str())
        .map(str::to_string);
    let name = doc.get("Name").and_then(|v| v.as_str()).map(str::to_string);

    let mut msg = Message::new(doc)
        .with_meta("url", base_url)
        .with_meta("endpoint", endpoint);
    if let Some(odata_id) = odata_id {
        msg = msg.with_meta("odata_id", odata_id);
    }
    if let Some(name) = name {
        msg = msg.with_meta("name", name);
    }
    msg
}
