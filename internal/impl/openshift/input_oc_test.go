package openshift

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/warpstreamlabs/bento/public/service"
)

func TestOpenShiftOCConfigParse(t *testing.T) {
	spec := openShiftOCInputConfig()

	tests := []struct {
		name        string
		config      string
		expectError bool
	}{
		{
			name: "collect pods",
			config: `
resources: [ pods ]
`,
		},
		{
			name: "collect multiple resources with selectors",
			config: `
resources: [ deploymentconfigs, routes ]
namespace: my-project
label_selector:
  app: backend
interval: 1m
`,
		},
		{
			name: "raw command mode",
			config: `
args: [ "adm", "top", "nodes", "--no-headers" ]
`,
		},
		{
			name: "explicit auth",
			config: `
resources: [ pods ]
server: https://api.cluster.example.com:6443
token: super-secret
insecure_skip_verify: true
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf, err := spec.ParseYAML(tt.config, nil)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, conf)
			}
		})
	}
}

func TestOpenShiftOCConfigValidation(t *testing.T) {
	spec := openShiftOCInputConfig()

	conf, err := spec.ParseYAML(`{}`, nil)
	require.NoError(t, err)
	_, err = newOpenShiftOCInput(conf, service.MockResources())
	require.Error(t, err)

	conf, err = spec.ParseYAML(`
resources: [ pods ]
args: [ "adm", "top", "nodes" ]
`, nil)
	require.NoError(t, err)
	_, err = newOpenShiftOCInput(conf, service.MockResources())
	require.Error(t, err)
}

func TestOCAuthAndQueryFlags(t *testing.T) {
	spec := openShiftOCInputConfig()
	conf, err := spec.ParseYAML(`
resources: [ pods ]
namespace: my-project
label_selector:
  app: backend
  env: prod
kubeconfig: /tmp/kubeconfig
context: my-context
server: https://api.cluster.example.com:6443
token: super-secret
insecure_skip_verify: true
`, nil)
	require.NoError(t, err)

	in, err := newOpenShiftOCInput(conf, service.MockResources())
	require.NoError(t, err)

	assert.Equal(t, []string{
		"--context=my-context",
		"--insecure-skip-tls-verify=true",
		"--kubeconfig=/tmp/kubeconfig",
		"--server=https://api.cluster.example.com:6443",
		"--token=super-secret",
	}, in.authFlags)
	assert.Equal(t, []string{
		"--namespace=my-project",
		"--selector=app=backend,env=prod",
	}, in.queryFlags)
}

func TestRedactArgs(t *testing.T) {
	redacted := redactArgs([]string{"get", "pods", "--token=super-secret", "--namespace=x"})
	assert.Equal(t, []string{"get", "pods", "--token=!!!SECRET_SCRUBBED!!!", "--namespace=x"}, redacted)
}

// writeFakeOC writes a shell script that emits the given payload on stdout,
// standing in for the real `oc` binary.
func writeFakeOC(t *testing.T, payload string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake oc script requires a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "oc")
	script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%v\nEOF\n", payload)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

const testPodList = `{
  "apiVersion": "v1",
  "kind": "PodList",
  "items": [
    {
      "apiVersion": "v1",
      "kind": "Pod",
      "metadata": {
        "name": "pod-a",
        "namespace": "my-project",
        "uid": "uid-a",
        "resourceVersion": "101",
        "creationTimestamp": "2026-01-02T03:04:05Z",
        "labels": { "app": "backend" },
        "annotations": { "team": "data" }
      }
    },
    {
      "apiVersion": "v1",
      "kind": "Pod",
      "metadata": {
        "name": "pod-b",
        "namespace": "my-project"
      }
    }
  ]
}`

func TestOpenShiftOCCollectsAndSplitsItems(t *testing.T) {
	spec := openShiftOCInputConfig()
	conf, err := spec.ParseYAML(fmt.Sprintf(`
resources: [ pods ]
binary_path: %v
interval: 0s
`, writeFakeOC(t, testPodList)), nil)
	require.NoError(t, err)

	in, err := newOpenShiftOCInput(conf, service.MockResources())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msg, ack, err := in.Read(ctx)
	require.NoError(t, err)
	require.NotNil(t, ack)

	structured, err := msg.AsStructured()
	require.NoError(t, err)
	name, _ := structured.(map[string]any)["metadata"].(map[string]any)["name"].(string)
	assert.Equal(t, "pod-a", name)

	assertMeta := func(key, expected string) {
		v, ok := msg.MetaGet(key)
		require.True(t, ok, "missing metadata %v", key)
		assert.Equal(t, expected, v)
	}
	assertMeta("openshift_resource", "pods")
	assertMeta("openshift_resource_kind", "Pod")
	assertMeta("openshift_resource_name", "pod-a")
	assertMeta("openshift_resource_namespace", "my-project")
	assertMeta("openshift_resource_uid", "uid-a")
	assertMeta("openshift_resource_version", "101")
	assertMeta("openshift_resource_creation_timestamp", "2026-01-02T03:04:05Z")
	assertMeta("openshift_labels_app", "backend")
	assertMeta("openshift_annotations_team", "data")

	msg, _, err = in.Read(ctx)
	require.NoError(t, err)
	v, ok := msg.MetaGet("openshift_resource_name")
	require.True(t, ok)
	assert.Equal(t, "pod-b", v)

	// interval 0s means the input ends after a single collection.
	_, _, err = in.Read(ctx)
	assert.ErrorIs(t, err, service.ErrEndOfInput)

	require.NoError(t, in.Close(ctx))
}

func TestOpenShiftOCNoSplit(t *testing.T) {
	spec := openShiftOCInputConfig()
	conf, err := spec.ParseYAML(fmt.Sprintf(`
resources: [ pods ]
binary_path: %v
split_items: false
interval: 0s
`, writeFakeOC(t, testPodList)), nil)
	require.NoError(t, err)

	in, err := newOpenShiftOCInput(conf, service.MockResources())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msg, _, err := in.Read(ctx)
	require.NoError(t, err)

	structured, err := msg.AsStructured()
	require.NoError(t, err)
	kind, _ := structured.(map[string]any)["kind"].(string)
	assert.Equal(t, "PodList", kind)

	_, _, err = in.Read(ctx)
	assert.ErrorIs(t, err, service.ErrEndOfInput)
}

func TestOpenShiftOCRawCommand(t *testing.T) {
	spec := openShiftOCInputConfig()
	conf, err := spec.ParseYAML(fmt.Sprintf(`
args: [ "adm", "top", "nodes", "--no-headers" ]
binary_path: %v
interval: 0s
`, writeFakeOC(t, "node-1   250m   4%   2Gi   12%")), nil)
	require.NoError(t, err)

	in, err := newOpenShiftOCInput(conf, service.MockResources())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msg, _, err := in.Read(ctx)
	require.NoError(t, err)

	b, err := msg.AsBytes()
	require.NoError(t, err)
	assert.Equal(t, "node-1   250m   4%   2Gi   12%", string(b))

	v, ok := msg.MetaGet("openshift_oc_args")
	require.True(t, ok)
	assert.Equal(t, "adm top nodes --no-headers", v)

	_, _, err = in.Read(ctx)
	assert.ErrorIs(t, err, service.ErrEndOfInput)
}

func TestOpenShiftOCCommandFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake oc script requires a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "oc")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho 'error: forbidden' >&2\nexit 1\n"), 0o755))

	spec := openShiftOCInputConfig()
	conf, err := spec.ParseYAML(fmt.Sprintf(`
resources: [ pods ]
binary_path: %v
interval: 0s
`, path), nil)
	require.NoError(t, err)

	in, err := newOpenShiftOCInput(conf, service.MockResources())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	_, _, err = in.Read(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestOpenShiftOCMissingBinary(t *testing.T) {
	spec := openShiftOCInputConfig()
	conf, err := spec.ParseYAML(`
resources: [ pods ]
binary_path: /does/not/exist/oc
`, nil)
	require.NoError(t, err)

	in, err := newOpenShiftOCInput(conf, service.MockResources())
	require.NoError(t, err)

	require.Error(t, in.Connect(context.Background()))
}
