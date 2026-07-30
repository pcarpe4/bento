use std::collections::BTreeMap;

use anyhow::Result;
use async_trait::async_trait;

use crate::message::Message;

/// An input polls a system and produces batches of messages.
#[async_trait]
pub trait Input: Send + Sync {
    async fn poll(&self) -> Result<Vec<Message>>;
}

/// A processor transforms, filters or annotates a batch in flight.
#[async_trait]
pub trait Processor: Send + Sync {
    async fn process(&self, batch: Vec<Message>) -> Result<Vec<Message>>;
}

/// An output delivers batches to an external system. `write` is retried by
/// the engine until it succeeds, so implementations should be idempotent or
/// tolerate replays.
#[async_trait]
pub trait Output: Send + Sync {
    async fn write(&self, batch: &[Message]) -> Result<()>;
    async fn close(&self) -> Result<()> {
        Ok(())
    }
}

pub type InputFactory = fn(&serde_yaml::Value) -> Result<Box<dyn Input>>;
pub type ProcessorFactory = fn(&serde_yaml::Value) -> Result<Box<dyn Processor>>;
pub type OutputFactory = fn(&serde_yaml::Value) -> Result<Box<dyn Output>>;

/// The registry of known component types. Adding a new component is one
/// module implementing the trait plus one line in `builtin()`.
pub struct Registry {
    inputs: BTreeMap<&'static str, InputFactory>,
    processors: BTreeMap<&'static str, ProcessorFactory>,
    outputs: BTreeMap<&'static str, OutputFactory>,
}

impl Registry {
    pub fn builtin() -> Self {
        let mut r = Self {
            inputs: BTreeMap::new(),
            processors: BTreeMap::new(),
            outputs: BTreeMap::new(),
        };

        r.register_input("generate", crate::inputs::generate::new);
        r.register_input("http_client", crate::inputs::http_client::new);
        r.register_input("redfish", crate::inputs::redfish::new);
        r.register_input("openshift_oc", crate::inputs::openshift_oc::new);
        r.register_input("vcenter", crate::inputs::vcenter::new);

        r.register_processor("filter", crate::processors::filter::new);
        r.register_processor("select", crate::processors::select::new);
        r.register_processor("set", crate::processors::set::new);
        r.register_processor("remove", crate::processors::remove::new);
        r.register_processor("set_meta", crate::processors::set_meta::new);

        r.register_output("stdout", crate::outputs::stdout::new);
        r.register_output("http_client", crate::outputs::http_client::new);
        r.register_output("mongodb", crate::outputs::mongodb::new);
        r
    }

    pub fn register_input(&mut self, name: &'static str, f: InputFactory) {
        if self.inputs.insert(name, f).is_some() {
            panic!("input type {name:?} registered twice");
        }
    }

    pub fn register_processor(&mut self, name: &'static str, f: ProcessorFactory) {
        if self.processors.insert(name, f).is_some() {
            panic!("processor type {name:?} registered twice");
        }
    }

    pub fn register_output(&mut self, name: &'static str, f: OutputFactory) {
        if self.outputs.insert(name, f).is_some() {
            panic!("output type {name:?} registered twice");
        }
    }

    pub fn new_input(&self, name: &str, cfg: &serde_yaml::Value) -> Result<Box<dyn Input>> {
        match self.inputs.get(name) {
            Some(f) => f(cfg),
            None => anyhow::bail!(
                "unknown input {:?}, available: {:?}",
                name,
                self.input_types()
            ),
        }
    }

    pub fn new_processor(&self, name: &str, cfg: &serde_yaml::Value) -> Result<Box<dyn Processor>> {
        match self.processors.get(name) {
            Some(f) => f(cfg),
            None => anyhow::bail!(
                "unknown processor {:?}, available: {:?}",
                name,
                self.processor_types()
            ),
        }
    }

    pub fn new_output(&self, name: &str, cfg: &serde_yaml::Value) -> Result<Box<dyn Output>> {
        match self.outputs.get(name) {
            Some(f) => f(cfg),
            None => anyhow::bail!(
                "unknown output {:?}, available: {:?}",
                name,
                self.output_types()
            ),
        }
    }

    pub fn input_types(&self) -> Vec<&'static str> {
        self.inputs.keys().copied().collect()
    }

    pub fn processor_types(&self) -> Vec<&'static str> {
        self.processors.keys().copied().collect()
    }

    pub fn output_types(&self) -> Vec<&'static str> {
        self.outputs.keys().copied().collect()
    }
}
