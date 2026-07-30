use std::time::Duration;

use anyhow::{bail, Context, Result};
use serde::Deserialize;

/// A single-entry map selecting a component type and its config, mirroring
/// the upstream config shape:
///
/// ```yaml
/// input:
///   http_client:
///     urls: [https://example.com]
/// ```
#[derive(Debug, Clone)]
pub struct PluginConf {
    pub type_name: String,
    pub config: serde_yaml::Value,
}

impl<'de> Deserialize<'de> for PluginConf {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        use serde::de::Error;
        let map = serde_yaml::Mapping::deserialize(deserializer)?;
        if map.len() != 1 {
            return Err(D::Error::custom(
                "expected exactly one component type key (e.g. `http_client: {...}`)",
            ));
        }
        let (key, value) = map.into_iter().next().expect("len checked above");
        let type_name = key
            .as_str()
            .ok_or_else(|| D::Error::custom("component type key must be a string"))?
            .to_string();
        Ok(PluginConf {
            type_name,
            config: strip_nulls(value),
        })
    }
}

/// Removes null-valued keys from mappings so that fields left empty by
/// `${VAR}` expansion (e.g. `password: `) fall back to their serde defaults
/// instead of failing to deserialize as strings.
fn strip_nulls(value: serde_yaml::Value) -> serde_yaml::Value {
    match value {
        serde_yaml::Value::Mapping(map) => serde_yaml::Value::Mapping(
            map.into_iter()
                .filter(|(_, v)| !v.is_null())
                .map(|(k, v)| (k, strip_nulls(v)))
                .collect(),
        ),
        serde_yaml::Value::Sequence(seq) => {
            serde_yaml::Value::Sequence(seq.into_iter().map(strip_nulls).collect())
        }
        other => other,
    }
}

#[derive(Debug, Default, Deserialize)]
pub struct PipelineConf {
    #[serde(default)]
    pub processors: Vec<PluginConf>,
}

/// One stream: input -> pipeline.processors -> output.
#[derive(Debug, Deserialize)]
pub struct StreamConf {
    pub input: PluginConf,
    #[serde(default)]
    pub pipeline: PipelineConf,
    pub output: PluginConf,
}

/// Substitutes `${VAR}` references with environment variable values. Unset
/// variables expand to an empty string. Plain `$VAR` is left untouched.
pub fn expand_env(raw: &str) -> String {
    let mut out = String::with_capacity(raw.len());
    let mut rest = raw;
    while let Some(start) = rest.find("${") {
        out.push_str(&rest[..start]);
        let after = &rest[start + 2..];
        match after.find('}') {
            Some(end)
                if !after[..end].is_empty()
                    && after[..end]
                        .chars()
                        .all(|c| c.is_ascii_alphanumeric() || c == '_') =>
            {
                out.push_str(&std::env::var(&after[..end]).unwrap_or_default());
                rest = &after[end + 1..];
            }
            _ => {
                out.push_str("${");
                rest = after;
            }
        }
    }
    out.push_str(rest);
    out
}

/// Parses durations of the form "300ms", "30s", "5m", "1h" or "0s".
pub fn parse_duration(s: &str) -> Result<Duration> {
    let s = s.trim();
    let (value, unit) = s
        .find(|c: char| !c.is_ascii_digit() && c != '.')
        .map(|i| s.split_at(i))
        .ok_or_else(|| anyhow::anyhow!("duration {s:?} is missing a unit (e.g. 30s, 5m)"))?;
    let value: f64 = value
        .parse()
        .with_context(|| format!("invalid duration {s:?}"))?;
    let secs = match unit {
        "ms" => value / 1000.0,
        "s" => value,
        "m" => value * 60.0,
        "h" => value * 3600.0,
        _ => bail!("invalid duration unit {unit:?} in {s:?}"),
    };
    Ok(Duration::from_secs_f64(secs))
}

/// Reads the optional `interval` field shared by all polling inputs.
/// Defaults to 5m; "0s" means poll once and shut the stream down.
pub fn interval_from(cfg: &serde_yaml::Value) -> Result<Duration> {
    match cfg.get("interval").and_then(|v| v.as_str()) {
        Some(s) => parse_duration(s).context("invalid interval"),
        None => Ok(Duration::from_secs(300)),
    }
}

/// Parses a stream config (after env expansion).
pub fn parse_stream(raw: &str) -> Result<StreamConf> {
    serde_yaml::from_str(&expand_env(raw)).context("failed to parse stream config")
}

/// Reads and parses a stream config file.
pub fn load_stream(path: &std::path::Path) -> Result<StreamConf> {
    let raw = std::fs::read_to_string(path)
        .with_context(|| format!("failed to read config {}", path.display()))?;
    parse_stream(&raw)
}

/// Loads every `*.yaml`/`*.yml` in a directory as an independent stream,
/// named by file stem.
pub fn load_streams_dir(dir: &std::path::Path) -> Result<Vec<(String, StreamConf)>> {
    let mut streams = Vec::new();
    let entries = std::fs::read_dir(dir)
        .with_context(|| format!("failed to read streams directory {}", dir.display()))?;
    let mut paths: Vec<_> = entries
        .filter_map(|e| e.ok())
        .map(|e| e.path())
        .filter(|p| {
            matches!(
                p.extension().and_then(|e| e.to_str()),
                Some("yaml") | Some("yml")
            )
        })
        .collect();
    paths.sort();

    for path in paths {
        let name = path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("stream")
            .to_string();
        let conf = load_stream(&path).with_context(|| format!("stream {}", path.display()))?;
        streams.push((name, conf));
    }
    if streams.is_empty() {
        bail!("no *.yaml stream configs found in {}", dir.display());
    }
    Ok(streams)
}
