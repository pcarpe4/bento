# Barebones Collector (Rust)

A minimal infrastructure data collector in Rust: poll **sources** on an
interval, write the results to **destinations**, all defined in one YAML
file. This is a port of the Go `collector/` module with the same config
format, extension model and OpenShift deployment story.

**~9MB release binary**, 10 direct crates (tokio, reqwest with rustls,
mongodb, serde/serde_json/serde_yaml, async-trait, anyhow, log,
env_logger). No OpenSSL — TLS is rustls, so the container base needs
nothing but libc.

## Built-in types

| Sources    | Collects                                                         |
|------------|------------------------------------------------------------------|
| `http_api` | Arbitrary JSON HTTP/REST endpoints (basic auth, custom headers)  |
| `redfish`  | BMC hardware inventory via Redfish (Dell iDRAC, HPE iLO, ...)    |
| `oc`       | OpenShift/Kubernetes resources via the `oc`/`kubectl` CLI        |
| `vcenter`  | VMware vCenter inventory via the vSphere Automation REST API     |

| Destinations | Writes to                          |
|--------------|------------------------------------|
| `stdout`     | JSON lines on standard output      |
| `http_post`  | POSTs each batch as JSON to a URL  |
| `mongodb`    | Inserts each message as a document |

Note the vCenter difference from the Go version: this source uses the
vSphere Automation **REST** API (vCenter 7.0+, base URL without `/sdk`) and
returns the list-endpoint summaries (name, power state, moref, ...) rather
than govmomi's deep property retrieval.

## Running

```bash
cargo build --release
./target/release/collector -config config.yaml   # see config.example.yaml
./target/release/collector -list                 # print registered types
```

## The config file

A **specific collection job** is one YAML entry: name it, pick a type, set
an interval and destinations, and fill in the type's config. Secrets are
injected via `${ENV_VAR}` references:

```yaml
default_interval: 5m

sources:
  - name: idrac-fleet            # a job: just a YAML entry
    type: redfish                # one of the registered source types
    interval: 15m                # 0s = collect once and exit
    destinations: [inventory]    # omit to send to all destinations
    config:
      urls: [https://idrac-01.example.com]
      username: svc-collector
      password: ${IDRAC_PASSWORD}

destinations:
  - name: inventory
    type: mongodb
    config:
      url: ${MONGO_URL}
      database: infra
      collection: inventory
```

Every message carries `meta.source` and `meta.source_type`, plus
source-specific metadata (host, resource name, ...) alongside the payload.

## Adding a new source type

One module in `src/sources/` implementing the `Source` trait, plus one
registration line:

```rust
// src/sources/ping.rs
use anyhow::{Context, Result};
use async_trait::async_trait;
use serde::Deserialize;

use crate::message::Message;
use crate::registry::Source;

#[derive(Deserialize)]
struct PingConfig {
    target: String,
}

struct PingSource {
    cfg: PingConfig,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Source>> {
    let cfg: PingConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    Ok(Box::new(PingSource { cfg }))
}

#[async_trait]
impl Source for PingSource {
    async fn collect(&self) -> Result<Vec<Message>> {
        let msg = Message::new(serde_json::json!({
            "target": self.cfg.target,
            "alive": true,
        }));
        Ok(vec![msg])
    }
}
```

Then in `src/registry.rs`, one line inside `Registry::builtin`:

```rust
r.register_source("ping", crate::sources::ping::new);
```

(and `pub mod ping;` in `src/sources/mod.rs`). Config parsing is plain
serde — declare a struct with defaults and it validates itself.
Destinations work the same way: implement `write` (and optionally `close`)
in `src/destinations/` and call `register_destination`.

## Running on OpenShift

The Dockerfile builds against rustls (no OpenSSL) on a distroless base and
runs as a non-root arbitrary UID, compatible with OpenShift's `restricted`
SCC out of the box:

```bash
oc new-build --binary --strategy=docker --name collector-rs
oc start-build collector-rs --from-dir . --follow
oc apply -f deploy/openshift.yaml
```

`deploy/openshift.yaml` mounts the config from a ConfigMap and injects
credentials from a Secret as environment variables, picked up via `${VAR}`
expansion. To use the `oc` source in-cluster, use an image that bundles the
`oc` binary and a ServiceAccount with the needed RBAC.

## What it deliberately doesn't do

No processors/transformations, no delivery guarantees or acknowledgements,
no buffering, no metrics endpoint — records are collected and handed to
destinations at-most-once per poll. Failures are logged and retried on the
next interval. When you outgrow that, use the full Bento build (or the
`collector/` Go module's bigger sibling, `cmd/bento-slim`).
