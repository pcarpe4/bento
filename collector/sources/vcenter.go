package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/pcarpe4/bento/collector/core"
)

// vcObjectKinds maps config-friendly object type names to vSphere managed
// object kinds and the properties collected for them.
var vcObjectKinds = map[string]struct {
	kind  string
	props []string
}{
	"virtual_machine": {"VirtualMachine", []string{"name", "summary", "runtime", "guest"}},
	"host_system":     {"HostSystem", []string{"name", "summary"}},
	"datastore":       {"Datastore", []string{"name", "summary"}},
	"cluster":         {"ClusterComputeResource", []string{"name", "summary"}},
	"resource_pool":   {"ResourcePool", []string{"name", "summary"}},
	"network":         {"Network", []string{"name", "summary"}},
	"datacenter":      {"Datacenter", []string{"name"}},
}

// vcenterSource collects inventory objects from a VMware vCenter or ESXi
// endpoint. It logs in per collection and out afterwards, keeping no state
// between polls.
type vcenterSource struct {
	url         *url.URL
	insecure    bool
	objectTypes []string
}

func init() {
	core.RegisterSource("vcenter", newVCenterSource)
}

func newVCenterSource(cfg core.Fields) (core.Source, error) {
	urlStr, err := cfg.RequiredString("url")
	if err != nil {
		return nil, err
	}
	u, err := soap.ParseURL(urlStr)
	if err != nil || u == nil {
		return nil, fmt.Errorf("invalid url %q: %v", urlStr, err)
	}
	if username := cfg.String("username", ""); username != "" {
		u.User = url.UserPassword(username, cfg.String("password", ""))
	}

	objectTypes := cfg.StringList("object_types")
	if len(objectTypes) == 0 {
		objectTypes = []string{"virtual_machine"}
	}
	for _, t := range objectTypes {
		if _, ok := vcObjectKinds[t]; !ok {
			return nil, fmt.Errorf("invalid object type %q", t)
		}
	}

	return &vcenterSource{
		url:         u,
		insecure:    cfg.Bool("insecure_skip_verify", false),
		objectTypes: objectTypes,
	}, nil
}

func (v *vcenterSource) Collect(ctx context.Context) ([]core.Message, error) {
	client, err := govmomi.NewClient(ctx, v.url, v.insecure)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to vCenter: %w", err)
	}
	defer func() { _ = client.Logout(ctx) }()

	manager := view.NewManager(client.Client)
	root := client.ServiceContent.RootFolder

	var batch []core.Message
	for _, objectType := range v.objectTypes {
		typeInfo := vcObjectKinds[objectType]

		containerView, err := manager.CreateContainerView(ctx, root, []string{typeInfo.kind}, true)
		if err != nil {
			return nil, fmt.Errorf("failed to create view for %v: %w", objectType, err)
		}

		var contents []types.ObjectContent
		err = containerView.Retrieve(ctx, []string{typeInfo.kind}, typeInfo.props, &contents)
		_ = containerView.Destroy(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve %v objects: %w", objectType, err)
		}

		for _, content := range contents {
			doc := map[string]any{
				"moRef": content.Obj.Value,
				"type":  content.Obj.Type,
			}
			var name string
			for _, prop := range content.PropSet {
				// Round-trip through JSON so payloads hold plain maps rather
				// than govmomi structs.
				raw, err := json.Marshal(prop.Val)
				if err != nil {
					continue
				}
				var val any
				_ = json.Unmarshal(raw, &val)
				doc[prop.Name] = val
				if prop.Name == "name" {
					name, _ = prop.Val.(string)
				}
			}

			msg := core.NewMessage(doc)
			msg.Meta["object_type"] = objectType
			msg.Meta["moref"] = content.Obj.String()
			if name != "" {
				msg.Meta["name"] = name
			}
			batch = append(batch, msg)
		}
	}
	return batch, nil
}
