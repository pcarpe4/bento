//! Built-in inputs. Adding a new input is one module here (implement
//! `Input`, expose a `new` factory) plus one line in `Registry::builtin`.
//! All polling inputs accept an `interval` field ("0s" = poll once).

pub mod generate;
pub mod http_client;
pub mod openshift_oc;
pub mod redfish;
pub mod vcenter;

use std::time::Duration;

use anyhow::Result;

/// Builds an HTTP client with the timeout and TLS settings shared by the
/// HTTP-based inputs.
pub(crate) fn build_http_client(timeout: Duration, insecure: bool) -> Result<reqwest::Client> {
    Ok(reqwest::Client::builder()
        .timeout(timeout)
        .danger_accept_invalid_certs(insecure)
        .build()?)
}

pub(crate) fn default_timeout() -> String {
    "15s".to_string()
}
