use std::collections::HashSet;
use std::time::Duration;

use anyhow::{bail, Context, Result};
use serde::Deserialize;

/// One entry under `sources` in the config file.
#[derive(Debug, Deserialize)]
pub struct SourceEntry {
    pub name: String,
    #[serde(rename = "type")]
    pub type_name: String,
    #[serde(default)]
    pub interval: Option<String>,
    #[serde(default)]
    pub destinations: Vec<String>,
    #[serde(default)]
    pub config: serde_yaml::Value,
}

/// One entry under `destinations` in the config file.
#[derive(Debug, Deserialize)]
pub struct DestinationEntry {
    pub name: String,
    #[serde(rename = "type")]
    pub type_name: String,
    #[serde(default)]
    pub config: serde_yaml::Value,
}

/// The root of the collector config file.
#[derive(Debug, Deserialize)]
pub struct Config {
    /// Applies to sources that don't set their own interval. "0s" means
    /// collect once and exit.
    #[serde(default = "default_interval")]
    pub default_interval: String,
    pub sources: Vec<SourceEntry>,
    pub destinations: Vec<DestinationEntry>,
}

fn default_interval() -> String {
    "5m".to_string()
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

/// Parses config YAML (after env expansion) and validates its structure.
pub fn parse(raw: &str) -> Result<Config> {
    let conf: Config = serde_yaml::from_str(&expand_env(raw)).context("failed to parse config")?;

    if conf.sources.is_empty() {
        bail!("at least one source must be configured");
    }
    if conf.destinations.is_empty() {
        bail!("at least one destination must be configured");
    }

    let mut dest_names = HashSet::new();
    for d in &conf.destinations {
        if d.name.is_empty() {
            bail!("every destination requires a name");
        }
        if !dest_names.insert(d.name.as_str()) {
            bail!("destination name {:?} used twice", d.name);
        }
    }

    let mut src_names = HashSet::new();
    for s in &conf.sources {
        if s.name.is_empty() {
            bail!("every source requires a name");
        }
        if !src_names.insert(s.name.as_str()) {
            bail!("source name {:?} used twice", s.name);
        }
        if let Some(interval) = &s.interval {
            parse_duration(interval)
                .with_context(|| format!("source {:?} has an invalid interval", s.name))?;
        }
        for d in &s.destinations {
            if !dest_names.contains(d.as_str()) {
                bail!("source {:?} references unknown destination {:?}", s.name, d);
            }
        }
    }
    parse_duration(&conf.default_interval).context("invalid default_interval")?;

    Ok(conf)
}

/// Reads, env-expands and parses a YAML config file.
pub fn load(path: &str) -> Result<Config> {
    let raw =
        std::fs::read_to_string(path).with_context(|| format!("failed to read config {path:?}"))?;
    parse(&raw)
}
