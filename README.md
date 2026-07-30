# bento-rs

A Rust port of the [Bento](https://github.com/warpstreamlabs/bento) stream
processor, focused (so far) on infrastructure data collection. Streams are
declared in YAML with the upstream shape — an **input**, a chain of
**processors**, and an **output** — and run on a small tokio engine with
retried, backoff-managed delivery.

```yaml
input:
  redfish:
    urls: [https://idrac-01.example.com]
    username: svc-collector
    password: ${IDRAC_PASSWORD}
    interval: 15m            # all polling inputs take interval; 0s = run once
pipeline:
  processors:
    - select:
        paths: [Model, Name, Status]
    - set_meta:
        key: model
        value: "${data:Model}"
output:
  mongodb:
    url: ${MONGO_URL}
    database: infra
    collection: inventory
```

## Running

```bash
cargo build --release
bento-rs run stream.yaml      # run one stream
bento-rs streams ./streams/   # run every *.yaml in a directory as a stream
bento-rs lint stream.yaml     # validate a config without running it
bento-rs list                 # print registered component types
```

Streams mode gives file-per-job modularity: each YAML file in the directory
is an independent stream named by its file stem, polled on its own interval.

## Components

| Inputs         | |
|----------------|---|
| `generate`     | Emits a static JSON document per poll (testing/heartbeat) |
| `http_client`  | Polls JSON HTTP/REST endpoints (basic auth, custom headers) |
| `redfish`      | BMC hardware inventory via Redfish (Dell iDRAC, HPE iLO, ...) with collection expansion |
| `openshift_oc` | OpenShift/Kubernetes resources via the `oc`/`kubectl` CLI |
| `vcenter`      | VMware vCenter inventory via the vSphere Automation REST API (7.0+) |

| Processors | |
|------------|---|
| `filter`   | Keep messages where a dot-path exists / equals a value |
| `select`   | Project messages down to a set of paths |
| `set`      | Set a field (supports `${meta:key}` interpolation) |
| `remove`   | Remove fields |
| `set_meta` | Set a metadata key (supports `${data:path}` interpolation) |

| Outputs       | |
|---------------|---|
| `stdout`      | JSON lines |
| `http_client` | POST batches as JSON |
| `mongodb`     | Insert documents (metadata under `_meta`) |

Every message carries `meta.stream` and `meta.input_type` plus
input-specific metadata. `${VAR}` in configs expands from the environment.

## Delivery semantics

At-least-once while the process lives: a failed output write is retried
with capped exponential backoff (1s → 60s) until it succeeds or the process
is asked to shut down. Polling inputs are snapshot-oriented, so a batch
lost to a crash is re-collected on the next interval. There is no on-disk
buffer yet (see roadmap).

## Extending

A new component is one module implementing a one-method async trait plus a
registration line in `src/registry.rs`:

```rust
// src/inputs/ping.rs
pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Input>> { ... }

#[async_trait]
impl Input for PingInput {
    async fn poll(&self) -> Result<Vec<Message>> { ... }
}
```

```rust
r.register_input("ping", crate::inputs::ping::new);
```

Config parsing is plain serde — declare a struct with defaults. Processors
(`process`) and outputs (`write`/`close`) follow the same pattern.

## Port status & roadmap

This is an early, honest subset of upstream Bento. What exists today is the
engine shape (streams, config format, component registries, retried
delivery) and the collection-oriented components above. Major upstream
features not yet ported, roughly in intended order:

- [ ] **Streaming inputs** — the engine is poll-based; long-lived consuming
      inputs (files, sockets, brokers) need an ack-driven input trait
- [ ] **Bloblang** — the `mapping` language; the `filter`/`set`/`select`
      processors are a placeholder for a real expression language
- [ ] **Streams REST API** — runtime add/remove/update of streams
- [ ] **Buffers** — durable on-disk buffering between input and output
- [ ] **Batching controls** — count/period/size-based batch shaping
- [ ] **Metrics** — Prometheus exporter and per-stream counters
- [ ] **Broker connectors** — Kafka, NATS, AMQP, MQTT inputs/outputs
- [ ] **Caches & rate limits** — shared named resources
- [ ] **Output switch/broker** — fan-out and conditional routing

Contributions to any box above are welcome — the component registry means
most of them land as self-contained modules.

## Running on OpenShift

The Dockerfile builds against rustls (no OpenSSL) on a distroless base and
runs as a non-root arbitrary UID, compatible with OpenShift's `restricted`
SCC:

```bash
oc new-build --binary --strategy=docker --name bento-rs
oc start-build bento-rs --from-dir . --follow
oc apply -f deploy/openshift.yaml
```

`deploy/openshift.yaml` mounts a ConfigMap at `/etc/bento/streams` (one key
per stream file) and injects credentials from a Secret via `${VAR}`
expansion — adding a collection job is a ConfigMap edit plus a rollout
restart, no rebuild.
