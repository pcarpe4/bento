use std::collections::BTreeMap;

use serde::Serialize;

/// A single collected record: a structured payload plus metadata describing
/// where it came from.
#[derive(Debug, Clone, Serialize)]
pub struct Message {
    pub data: serde_json::Value,
    pub meta: BTreeMap<String, String>,
}

impl Message {
    pub fn new(data: serde_json::Value) -> Self {
        Self {
            data,
            meta: BTreeMap::new(),
        }
    }

    pub fn with_meta(mut self, key: &str, value: impl Into<String>) -> Self {
        self.meta.insert(key.to_string(), value.into());
        self
    }
}
