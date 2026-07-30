//! Built-in outputs. Adding a new output is one module here (implement
//! `Output`, expose a `new` factory) plus one line in `Registry::builtin`.

pub mod http_client;
pub mod mongodb;
pub mod stdout;
