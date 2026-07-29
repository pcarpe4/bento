// Command bento-slim is a trimmed Bento build containing only the components
// needed for infrastructure collection pipelines. It keeps the full engine —
// streams mode, the runtime streams REST API, acknowledgements/retries and
// Bloblang — while compiling in a fraction of the connectors, which shrinks
// the binary and its dependency surface considerably.
//
// To add or remove component families, edit the blank imports below and
// rebuild. Any package under public/components can be included.
package main

import (
	"context"

	"github.com/warpstreamlabs/bento/public/service"

	// Core: pure processors, Bloblang, batching, brokers, generate, etc.
	_ "github.com/warpstreamlabs/bento/public/components/pure"

	// IO: http_client, http_server, file, socket, stdin/stdout, subprocess.
	_ "github.com/warpstreamlabs/bento/public/components/io"

	// Collection sources.
	_ "github.com/warpstreamlabs/bento/public/components/kubernetes"
	_ "github.com/warpstreamlabs/bento/public/components/openshift"
	_ "github.com/warpstreamlabs/bento/public/components/redfish"
	_ "github.com/warpstreamlabs/bento/public/components/vmware"

	// Destinations.
	_ "github.com/warpstreamlabs/bento/public/components/mongodb"

	// Metrics: the prometheus exporter, scrapeable on /metrics.
	_ "github.com/warpstreamlabs/bento/public/components/prometheus"
)

var (
	// Version version set at compile time.
	Version string
	// DateBuilt date built set at compile time.
	DateBuilt string
	// BinaryName binary name.
	BinaryName string = "bento-slim"
)

func main() {
	service.RunCLI(
		context.Background(),
		service.CLIOptSetVersion(Version, DateBuilt),
		service.CLIOptSetBinaryName(BinaryName),
		service.CLIOptSetProductName("Bento"),
		service.CLIOptSetDocumentationURL("https://warpstreamlabs.github.io/bento/docs"),
		service.CLIOptSetShowRunCommand(true),
	)
}
