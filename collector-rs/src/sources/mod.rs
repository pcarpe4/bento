//! Built-in source types. Adding a new source type means adding one module
//! here (implement `Source`, expose a `new` factory) plus one line in
//! `Registry::builtin`.

pub mod http_api;
pub mod oc;
pub mod redfish;
pub mod vcenter;

use std::time::Duration;

use anyhow::Result;

/// Builds an HTTP client with the timeout and TLS settings shared by the
/// HTTP-based sources.
pub(crate) fn http_client(timeout: Duration, insecure: bool) -> Result<reqwest::Client> {
    Ok(reqwest::Client::builder()
        .timeout(timeout)
        .danger_accept_invalid_certs(insecure)
        .build()?)
}

pub(crate) fn default_timeout() -> String {
    "15s".to_string()
}
