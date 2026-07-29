use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::config::parse_duration;
use crate::message::Message;
use crate::registry::Source;

fn default_binary_path() -> String {
    "oc".to_string()
}

fn default_oc_timeout() -> String {
    "30s".to_string()
}

/// Collects resources from an OpenShift or Kubernetes cluster by executing
/// `oc get <resource> -o json` (works with kubectl too).
#[derive(Deserialize)]
struct OcConfig {
    resources: Vec<String>,
    #[serde(default = "default_binary_path")]
    binary_path: String,
    #[serde(default)]
    namespace: String,
    #[serde(default)]
    all_namespaces: bool,
    #[serde(default)]
    label_selector: String,
    #[serde(default)]
    kubeconfig: String,
    #[serde(default)]
    context: String,
    #[serde(default)]
    server: String,
    #[serde(default)]
    token: String,
    #[serde(default)]
    insecure_skip_verify: bool,
    #[serde(default = "default_oc_timeout")]
    timeout: String,
}

struct OcSource {
    binary_path: String,
    resources: Vec<String>,
    flags: Vec<String>,
    timeout: std::time::Duration,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Source>> {
    let cfg: OcConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    if cfg.resources.is_empty() {
        bail!("config field \"resources\" is required");
    }

    let mut flags = Vec::new();
    for (value, flag) in [
        (&cfg.namespace, "--namespace"),
        (&cfg.kubeconfig, "--kubeconfig"),
        (&cfg.context, "--context"),
        (&cfg.server, "--server"),
        (&cfg.token, "--token"),
        (&cfg.label_selector, "--selector"),
    ] {
        if !value.is_empty() {
            flags.push(format!("{flag}={value}"));
        }
    }
    if cfg.all_namespaces {
        flags.push("--all-namespaces".to_string());
    }
    if cfg.insecure_skip_verify {
        flags.push("--insecure-skip-tls-verify=true".to_string());
    }

    Ok(Box::new(OcSource {
        binary_path: cfg.binary_path,
        resources: cfg.resources,
        flags,
        timeout: parse_duration(&cfg.timeout)?,
    }))
}

#[async_trait]
impl Source for OcSource {
    async fn collect(&self) -> Result<Vec<Message>> {
        let mut batch = Vec::new();
        for resource in &self.resources {
            let mut cmd = tokio::process::Command::new(&self.binary_path);
            cmd.arg("get")
                .arg(resource)
                .arg("--output=json")
                .args(&self.flags)
                .kill_on_drop(true);

            let output = tokio::time::timeout(self.timeout, cmd.output())
                .await
                .map_err(|_| anyhow::anyhow!("{} get {resource} timed out", self.binary_path))?
                .with_context(|| format!("failed to execute {}", self.binary_path))?;

            if !output.status.success() {
                let detail = redact_token(String::from_utf8_lossy(&output.stderr).trim());
                bail!("{} get {resource} failed: {detail}", self.binary_path);
            }

            let doc: serde_json::Value =
                serde_json::from_slice(&output.stdout).with_context(|| {
                    format!("failed to parse {} get {resource} output", self.binary_path)
                })?;

            match doc.get("items").and_then(|v| v.as_array()) {
                Some(items) => {
                    for item in items.clone() {
                        batch.push(message(resource, item));
                    }
                }
                None => batch.push(message(resource, doc)),
            }
        }
        Ok(batch)
    }
}

/// Hides token values from error output.
fn redact_token(s: &str) -> String {
    match s.find("--token=") {
        Some(start) => {
            let end = s[start..]
                .find(char::is_whitespace)
                .map(|i| start + i)
                .unwrap_or(s.len());
            format!("{}--token=!!!SECRET_SCRUBBED!!!{}", &s[..start], &s[end..])
        }
        None => s.to_string(),
    }
}

fn message(resource: &str, doc: serde_json::Value) -> Message {
    let mut msg = Message::new(serde_json::Value::Null).with_meta("resource", resource);
    if let Some(kind) = doc.get("kind").and_then(|v| v.as_str()) {
        msg = msg.with_meta("kind", kind);
    }
    if let Some(metadata) = doc.get("metadata") {
        if let Some(name) = metadata.get("name").and_then(|v| v.as_str()) {
            msg = msg.with_meta("name", name);
        }
        if let Some(namespace) = metadata.get("namespace").and_then(|v| v.as_str()) {
            msg = msg.with_meta("namespace", namespace);
        }
    }
    msg.data = doc;
    msg
}
