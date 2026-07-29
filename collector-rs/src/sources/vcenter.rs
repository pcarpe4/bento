use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::config::parse_duration;
use crate::message::Message;
use crate::registry::Source;

/// Maps config-friendly object type names to vSphere Automation REST API
/// list endpoints.
const OBJECT_TYPES: &[(&str, &str)] = &[
    ("virtual_machine", "/api/vcenter/vm"),
    ("host_system", "/api/vcenter/host"),
    ("datastore", "/api/vcenter/datastore"),
    ("cluster", "/api/vcenter/cluster"),
    ("resource_pool", "/api/vcenter/resource-pool"),
    ("network", "/api/vcenter/network"),
    ("datacenter", "/api/vcenter/datacenter"),
];

fn default_object_types() -> Vec<String> {
    vec!["virtual_machine".to_string()]
}

/// Collects inventory summaries from a VMware vCenter endpoint via the
/// vSphere Automation REST API (vCenter 7.0+). Logs in per collection and
/// out afterwards, keeping no state between polls.
#[derive(Deserialize)]
struct VCenterConfig {
    /// Base URL of vCenter, e.g. "https://vcenter.example.com" (no /sdk —
    /// this source uses the REST API, not SOAP).
    url: String,
    username: String,
    #[serde(default)]
    password: String,
    #[serde(default = "default_object_types")]
    object_types: Vec<String>,
    #[serde(default)]
    insecure_skip_verify: bool,
    #[serde(default = "super::default_timeout")]
    timeout: String,
}

struct VCenterSource {
    cfg: VCenterConfig,
    client: reqwest::Client,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Source>> {
    let mut cfg: VCenterConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.url.is_empty() {
        bail!("config field \"url\" is required");
    }
    if cfg.username.is_empty() {
        bail!("config field \"username\" is required");
    }
    while cfg.url.ends_with('/') {
        cfg.url.pop();
    }
    for t in &cfg.object_types {
        if !OBJECT_TYPES.iter().any(|(name, _)| name == t) {
            bail!(
                "invalid object type {:?}, must be one of {:?}",
                t,
                OBJECT_TYPES.iter().map(|(n, _)| *n).collect::<Vec<_>>()
            );
        }
    }
    let client = super::http_client(parse_duration(&cfg.timeout)?, cfg.insecure_skip_verify)?;
    Ok(Box::new(VCenterSource { cfg, client }))
}

#[async_trait]
impl Source for VCenterSource {
    async fn collect(&self) -> Result<Vec<Message>> {
        let session = self.login().await?;
        let result = self.collect_with_session(&session).await;
        self.logout(&session).await;
        result
    }
}

impl VCenterSource {
    async fn login(&self) -> Result<String> {
        let url = format!("{}/api/session", self.cfg.url);
        let res = self
            .client
            .post(&url)
            .basic_auth(&self.cfg.username, Some(&self.cfg.password))
            .send()
            .await
            .with_context(|| format!("failed to connect to vCenter at {}", self.cfg.url))?;
        if !res.status().is_success() {
            bail!("vCenter session login failed: status {}", res.status());
        }
        // The session endpoint returns the token as a JSON string body.
        res.json::<String>()
            .await
            .context("failed to parse vCenter session token")
    }

    async fn logout(&self, session: &str) {
        let url = format!("{}/api/session", self.cfg.url);
        let _ = self
            .client
            .delete(&url)
            .header("vmware-api-session-id", session)
            .send()
            .await;
    }

    async fn collect_with_session(&self, session: &str) -> Result<Vec<Message>> {
        let mut batch = Vec::new();
        for object_type in &self.cfg.object_types {
            let endpoint = OBJECT_TYPES
                .iter()
                .find(|(name, _)| name == object_type)
                .map(|(_, path)| *path)
                .expect("object types validated in constructor");

            let url = format!("{}{endpoint}", self.cfg.url);
            let res = self
                .client
                .get(&url)
                .header("vmware-api-session-id", session)
                .send()
                .await
                .with_context(|| format!("request for {url} failed"))?;
            if !res.status().is_success() {
                bail!("request for {url} failed: status {}", res.status());
            }
            let items: Vec<serde_json::Value> = res
                .json()
                .await
                .with_context(|| format!("failed to parse response for {url}"))?;

            for item in items {
                let name = item
                    .get("name")
                    .and_then(|v| v.as_str())
                    .map(str::to_string);
                // The identifier field varies by type: "vm", "host", ...
                let moref = [
                    "vm",
                    "host",
                    "datastore",
                    "cluster",
                    "resource_pool",
                    "network",
                    "datacenter",
                ]
                .iter()
                .find_map(|key| item.get(*key)?.as_str().map(str::to_string));

                let mut msg = Message::new(item).with_meta("object_type", object_type.clone());
                if let Some(name) = name {
                    msg = msg.with_meta("name", name);
                }
                if let Some(moref) = moref {
                    msg = msg.with_meta("moref", moref);
                }
                batch.push(msg);
            }
        }
        Ok(batch)
    }
}
