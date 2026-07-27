# Barebones Collector

A minimal infrastructure data collector: poll **sources** on an interval,
write the results to **destinations**, all defined in one YAML file. It is a
deliberately tiny alternative to the full Bento pipeline for
collection-only workloads.

**Total dependency count: 3 direct modules** (`govmomi` for vCenter,
`mongo-driver` for MongoDB, `yaml.v3` for config) plus their handful of
transitive dependencies — roughly a dozen modules in `go.mod`, compared to
~450 for the full Bento build. Everything else is the Go standard library.
This directory is its own Go module and does not import Bento at all.

## Built-in types

| Sources    | Collects                                                        |
|------------|-----------------------------------------------------------------|
| `http_api` | Arbitrary JSON HTTP/REST endpoints (basic auth, custom headers) |
| `redfish`  | BMC hardware inventory via Redfish (Dell iDRAC, HPE iLO, ...)   |
| `oc`       | OpenShift/Kubernetes resources via the `oc`/`kubectl` CLI       |
| `vcenter`  | VMware vCenter/ESXi inventory objects                           |

| Destinations | Writes to                                |
|--------------|------------------------------------------|
| `stdout`     | JSON lines on standard output            |
| `http_post`  | POSTs each batch as JSON to a URL        |
| `mongodb`    | Inserts each message as a document       |

## Running

```bash
go build -o collector .
./collector -config config.yaml   # see config.example.yaml
./collector -list                 # print registered types
```

## The config file

A **specific source** is one YAML entry: name it, pick a type, set an
interval and destinations, and fill in the type's config. Secrets are
injected via `${ENV_VAR}` references:

```yaml
default_interval: 5m

sources:
  - name: idrac-fleet          # a specific source: just a YAML entry
    type: redfish              # one of the registered source types
    interval: 15m              # 0s = collect once and exit
    destinations: [inventory]  # omit to send to all destinations
    config:
      urls: [https://idrac-01.example.com]
      username: svc-collector
      password: ${IDRAC_PASSWORD}
      insecure_skip_verify: true

destinations:
  - name: inventory
    type: mongodb
    config:
      url: ${MONGO_URL}
      database: infra
      collection: inventory
```

Every message carries `meta.source` and `meta.source_type` so destinations
can tell records apart; sources add their own metadata (host, resource
name, etc.) alongside the payload.

## Adding a new source type

One file in `sources/`: implement `core.Source` and register it in `init`.

```go
package sources

import (
    "context"

    "github.com/pcarpe4/bento/collector/core"
)

type myPingSource struct {
    target string
}

func init() {
    core.RegisterSource("ping", func(cfg core.Fields) (core.Source, error) {
        target, err := cfg.RequiredString("target")
        if err != nil {
            return nil, err
        }
        return &myPingSource{target: target}, nil
    })
}

func (p *myPingSource) Collect(ctx context.Context) ([]core.Message, error) {
    msg := core.NewMessage(map[string]any{"target": p.target, "alive": true})
    return []core.Message{msg}, nil
}
```

That's it — `type: ping` is now usable in the config. Destinations work the
same way in `destinations/`, implementing `Write` and `Close` and calling
`core.RegisterDestination`. The `core.Fields` helpers (`RequiredString`,
`String`, `StringList`, `StringMap`, `Bool`, `Duration`) cover typical
config parsing without a schema framework.

## Running on OpenShift

The Dockerfile produces a static binary on a `scratch` base that runs as a
non-root arbitrary UID, compatible with OpenShift's `restricted` SCC out of
the box:

```bash
# From this directory, using the in-cluster build system:
oc new-build --binary --strategy=docker --name collector
oc start-build collector --from-dir . --follow

# Then deploy config + secret + deployment (edit first):
oc apply -f deploy/openshift.yaml
```

`deploy/openshift.yaml` mounts the config from a ConfigMap and injects
credentials from a Secret as environment variables, which the config file
picks up through `${VAR}` expansion. To collect from the cluster the pod
runs in with the `oc` source, add a ServiceAccount with the needed RBAC and
point the source at its projected token (or use an image that bundles the
`oc` binary).

## What it deliberately doesn't do

No processors/transformations, no delivery guarantees or acknowledgements,
no buffering, no metrics endpoint, no config linting, no streaming inputs —
records are collected and handed to destinations at-most-once per poll. If
a collection or write fails it is logged and retried on the next interval.
When you outgrow that, that's what the full Bento build is for.
