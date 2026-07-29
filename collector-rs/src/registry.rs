use std::collections::BTreeMap;

use anyhow::Result;
use async_trait::async_trait;

use crate::message::Message;

/// A source collects a batch of messages. Constructed once at startup,
/// `collect` is called on every poll.
#[async_trait]
pub trait Source: Send + Sync {
    async fn collect(&self) -> Result<Vec<Message>>;
}

/// A destination receives batches of collected messages.
#[async_trait]
pub trait Destination: Send + Sync {
    async fn write(&self, batch: &[Message]) -> Result<()>;
    async fn close(&self) -> Result<()> {
        Ok(())
    }
}

/// Builds a source from its YAML `config` block.
pub type SourceFactory = fn(&serde_yaml::Value) -> Result<Box<dyn Source>>;

/// Builds a destination from its YAML `config` block.
pub type DestinationFactory = fn(&serde_yaml::Value) -> Result<Box<dyn Destination>>;

/// The registry of known source and destination types. Adding a new type is
/// one line in `builtin()` plus a module implementing the trait.
pub struct Registry {
    sources: BTreeMap<&'static str, SourceFactory>,
    destinations: BTreeMap<&'static str, DestinationFactory>,
}

impl Registry {
    /// Returns the registry of built-in types.
    pub fn builtin() -> Self {
        let mut r = Self {
            sources: BTreeMap::new(),
            destinations: BTreeMap::new(),
        };
        r.register_source("http_api", crate::sources::http_api::new);
        r.register_source("redfish", crate::sources::redfish::new);
        r.register_source("oc", crate::sources::oc::new);
        r.register_source("vcenter", crate::sources::vcenter::new);
        r.register_destination("stdout", crate::destinations::stdout::new);
        r.register_destination("http_post", crate::destinations::http_post::new);
        r.register_destination("mongodb", crate::destinations::mongodb::new);
        r
    }

    pub fn register_source(&mut self, name: &'static str, factory: SourceFactory) {
        if self.sources.insert(name, factory).is_some() {
            panic!("source type {name:?} registered twice");
        }
    }

    pub fn register_destination(&mut self, name: &'static str, factory: DestinationFactory) {
        if self.destinations.insert(name, factory).is_some() {
            panic!("destination type {name:?} registered twice");
        }
    }

    pub fn new_source(&self, type_name: &str, cfg: &serde_yaml::Value) -> Result<Box<dyn Source>> {
        match self.sources.get(type_name) {
            Some(factory) => factory(cfg),
            None => anyhow::bail!(
                "unknown source type {:?}, available: {:?}",
                type_name,
                self.source_types()
            ),
        }
    }

    pub fn new_destination(
        &self,
        type_name: &str,
        cfg: &serde_yaml::Value,
    ) -> Result<Box<dyn Destination>> {
        match self.destinations.get(type_name) {
            Some(factory) => factory(cfg),
            None => anyhow::bail!(
                "unknown destination type {:?}, available: {:?}",
                type_name,
                self.destination_types()
            ),
        }
    }

    pub fn source_types(&self) -> Vec<&'static str> {
        self.sources.keys().copied().collect()
    }

    pub fn destination_types(&self) -> Vec<&'static str> {
        self.destinations.keys().copied().collect()
    }
}
