//! Built-in destination types. Adding a new destination type means adding
//! one module here (implement `Destination`, expose a `new` factory) plus
//! one line in `Registry::builtin`.

pub mod http_post;
pub mod mongodb;
pub mod stdout;
