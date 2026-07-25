package vmware

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/simulator"

	"github.com/warpstreamlabs/bento/public/service"
)

func TestVCenterConfigParse(t *testing.T) {
	spec := vCenterInputConfig()

	tests := []struct {
		name        string
		config      string
		expectError bool
	}{
		{
			name: "defaults",
			config: `
url: https://vcenter.example.com/sdk
`,
		},
		{
			name: "full config",
			config: `
url: https://vcenter.example.com/sdk
username: administrator@vsphere.local
password: hunter2
insecure_skip_verify: true
object_types: [ virtual_machine, host_system, datastore ]
properties: [ name, summary.runtime.powerState ]
datacenter: DC0
interval: 1m
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

func TestVCenterConfigValidation(t *testing.T) {
	spec := vCenterInputConfig()

	conf, err := spec.ParseYAML(`
url: https://vcenter.example.com/sdk
object_types: [ not_a_thing ]
`, nil)
	require.NoError(t, err)
	_, err = newVCenterInput(conf, service.MockResources())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid object type")

	conf, err = spec.ParseYAML(`
url: https://vcenter.example.com/sdk
object_types: []
`, nil)
	require.NoError(t, err)
	_, err = newVCenterInput(conf, service.MockResources())
	require.Error(t, err)
}

func TestEnsureNameProp(t *testing.T) {
	assert.Equal(t, []string{"name", "summary"}, ensureNameProp([]string{"summary"}))
	assert.Equal(t, []string{"name", "summary"}, ensureNameProp([]string{"name", "summary"}))
}

func TestVCenterCollectsInventory(t *testing.T) {
	model := simulator.VPX()
	defer model.Remove()
	require.NoError(t, model.Create())

	server := model.Service.NewServer()
	defer server.Close()

	spec := vCenterInputConfig()
	conf, err := spec.ParseYAML(fmt.Sprintf(`
url: %v
object_types: [ virtual_machine, host_system, datastore ]
interval: 0s
`, server.URL.String()), nil)
	require.NoError(t, err)

	in, err := newVCenterInput(conf, service.MockResources())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	counts := map[string]int{}
	for {
		msg, ack, err := in.Read(ctx)
		if err != nil {
			assert.ErrorIs(t, err, service.ErrEndOfInput)
			break
		}
		require.NotNil(t, ack)

		objectType, ok := msg.MetaGet("vcenter_object_type")
		require.True(t, ok)
		counts[objectType]++

		moref, ok := msg.MetaGet("vcenter_moref")
		require.True(t, ok)
		assert.NotEmpty(t, moref)

		name, ok := msg.MetaGet("vcenter_name")
		require.True(t, ok)
		assert.NotEmpty(t, name)

		b, err := msg.AsBytes()
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(b, &doc))
		assert.Equal(t, name, doc["name"])
		assert.NotEmpty(t, doc["moRef"])
		if objectType != "datacenter" {
			assert.Contains(t, doc, "summary")
		}
	}

	assert.Positive(t, counts["virtual_machine"], "expected at least one virtual machine")
	assert.Positive(t, counts["host_system"], "expected at least one host system")
	assert.Positive(t, counts["datastore"], "expected at least one datastore")

	require.NoError(t, in.Close(ctx))
}

func TestVCenterCustomProperties(t *testing.T) {
	model := simulator.VPX()
	defer model.Remove()
	require.NoError(t, model.Create())

	server := model.Service.NewServer()
	defer server.Close()

	spec := vCenterInputConfig()
	conf, err := spec.ParseYAML(fmt.Sprintf(`
url: %v
object_types: [ virtual_machine ]
properties: [ "summary.runtime.powerState" ]
datacenter: DC0
interval: 0s
`, server.URL.String()), nil)
	require.NoError(t, err)

	in, err := newVCenterInput(conf, service.MockResources())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msg, _, err := in.Read(ctx)
	require.NoError(t, err)

	b, err := msg.AsBytes()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(b, &doc))
	assert.Contains(t, doc, "name")
	assert.Contains(t, doc, "summary.runtime.powerState")

	require.NoError(t, in.Close(ctx))
}

func TestVCenterUnreachableEndpoint(t *testing.T) {
	model := simulator.VPX()
	require.NoError(t, model.Create())

	server := model.Service.NewServer()
	endpoint := server.URL.String()

	// Shut everything down so the endpoint no longer accepts connections.
	server.Close()
	model.Remove()

	spec := vCenterInputConfig()
	conf, err := spec.ParseYAML(fmt.Sprintf(`
url: %v
username: user
password: pass
interval: 0s
`, endpoint), nil)
	require.NoError(t, err)

	in, err := newVCenterInput(conf, service.MockResources())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.Error(t, in.Connect(ctx))
}
