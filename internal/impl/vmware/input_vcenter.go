package vmware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/warpstreamlabs/bento/public/service"
)

const (
	vciFieldURL                = "url"
	vciFieldUsername           = "username"
	vciFieldPassword           = "password"
	vciFieldInsecureSkipVerify = "insecure_skip_verify"
	vciFieldObjectTypes        = "object_types"
	vciFieldProperties         = "properties"
	vciFieldDatacenter         = "datacenter"
	vciFieldInterval           = "interval"
)

// vcObjectType maps a config-friendly object type name to the vSphere managed
// object kind and the default set of properties collected for it.
type vcObjectType struct {
	kind         string
	defaultProps []string
}

var vcObjectTypes = map[string]vcObjectType{
	"virtual_machine": {kind: "VirtualMachine", defaultProps: []string{"name", "summary", "runtime", "guest"}},
	"host_system":     {kind: "HostSystem", defaultProps: []string{"name", "summary"}},
	"datastore":       {kind: "Datastore", defaultProps: []string{"name", "summary"}},
	"cluster":         {kind: "ClusterComputeResource", defaultProps: []string{"name", "summary"}},
	"resource_pool":   {kind: "ResourcePool", defaultProps: []string{"name", "summary"}},
	"network":         {kind: "Network", defaultProps: []string{"name", "summary"}},
	"datacenter":      {kind: "Datacenter", defaultProps: []string{"name"}},
}

func vcObjectTypeNames() []string {
	names := make([]string, 0, len(vcObjectTypes))
	for name := range vcObjectTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func vCenterInputConfig() *service.ConfigSpec {
	return service.NewConfigSpec().
		Beta().
		Categories("Services").
		Version("1.20.0").
		Summary("Collects inventory objects from a VMware vCenter (or standalone ESXi) endpoint on an interval, emitting each object as a JSON message.").
		Description(`
This input connects to the vSphere API and periodically retrieves the
configured inventory object types, emitting one message per object. It is
useful for feeding CMDBs, asset inventories and monitoring pipelines with
virtual machine, host and datastore state:

` + "```yaml" + `
input:
  vcenter:
    url: https://vcenter.example.com/sdk
    username: administrator@vsphere.local
    password: "${VSPHERE_PASSWORD}"
    object_types: [ virtual_machine, host_system, datastore ]
    interval: 5m
` + "```" + `

By default a sensible set of properties is collected per object type (such as
` + "`name`" + ` and ` + "`summary`" + `). The ` + "`properties`" + ` field overrides this
with an explicit list of property paths applied to all configured object
types.

Set ` + "`interval`" + ` to ` + "`0s`" + ` to collect exactly once and then shut the
input down, which is useful for batch-style jobs.

### Metadata

This input adds the following metadata fields to each message:

` + "```text" + `
- vcenter_object_type
- vcenter_moref
- vcenter_name
` + "```" + `

You can access these metadata fields using
[function interpolation](/docs/configuration/interpolation#bloblang-queries).
`).
		Field(service.NewStringField(vciFieldURL).
			Description("The URL of the vCenter or ESXi SDK endpoint.").
			Example("https://vcenter.example.com/sdk")).
		Field(service.NewStringField(vciFieldUsername).
			Description("The username to authenticate with.").
			Default("").
			Example("administrator@vsphere.local")).
		Field(service.NewStringField(vciFieldPassword).
			Description("The password to authenticate with.").
			Default("").
			Secret()).
		Field(service.NewBoolField(vciFieldInsecureSkipVerify).
			Description("Skip TLS certificate verification. Not recommended for production.").
			Default(false).
			Advanced()).
		Field(service.NewStringListField(vciFieldObjectTypes).
			Description("The inventory object types to collect. Valid values are `" + fmt.Sprintf("%v", vcObjectTypeNames()) + "`.").
			Default([]any{"virtual_machine"}).
			Example([]string{"virtual_machine", "host_system", "datastore"}).
			LintRule(`root = if this.type() == "array" {
				this.filter(v -> ![` + vcObjectTypeNamesLint() + `].contains(v)).map_each(v -> "invalid object type %q".format(v))
			} else if ![` + vcObjectTypeNamesLint() + `].contains(this) {
				"invalid object type %q".format(this)
			}`)).
		Field(service.NewStringListField(vciFieldProperties).
			Description("An explicit list of property paths to collect for all configured object types, overriding the per-type defaults. The `name` property is always included.").
			Default([]any{}).
			Example([]string{"name", "summary.runtime.powerState", "guest.ipAddress"}).
			Advanced()).
		Field(service.NewStringField(vciFieldDatacenter).
			Description("An optional datacenter name to restrict collection to. When empty all datacenters are collected.").
			Default("").
			Advanced()).
		Field(service.NewDurationField(vciFieldInterval).
			Description("The time between collections. Set to `0s` to collect exactly once and then shut down.").
			Default("5m"))
}

func vcObjectTypeNamesLint() string {
	var out string
	for i, name := range vcObjectTypeNames() {
		if i > 0 {
			out += ", "
		}
		out += `"` + name + `"`
	}
	return out
}

func init() {
	err := service.RegisterInput(
		"vcenter", vCenterInputConfig(),
		func(conf *service.ParsedConfig, mgr *service.Resources) (service.Input, error) {
			return newVCenterInput(conf, mgr)
		})
	if err != nil {
		panic(err)
	}
}

//------------------------------------------------------------------------------

type vCenterInput struct {
	log *service.Logger

	url                *url.URL
	insecureSkipVerify bool
	objectTypes        []string
	properties         []string
	datacenter         string
	interval           time.Duration

	client *govmomi.Client

	pending  []*service.Message
	nextPoll time.Time
	polled   bool
}

func newVCenterInput(conf *service.ParsedConfig, mgr *service.Resources) (*vCenterInput, error) {
	v := &vCenterInput{
		log: mgr.Logger(),
	}

	urlStr, err := conf.FieldString(vciFieldURL)
	if err != nil {
		return nil, err
	}
	if v.url, err = soap.ParseURL(urlStr); err != nil {
		return nil, fmt.Errorf("failed to parse url: %w", err)
	}
	if v.url == nil {
		return nil, fmt.Errorf("invalid url: %v", urlStr)
	}

	username, err := conf.FieldString(vciFieldUsername)
	if err != nil {
		return nil, err
	}
	password, err := conf.FieldString(vciFieldPassword)
	if err != nil {
		return nil, err
	}
	if username != "" {
		v.url.User = url.UserPassword(username, password)
	}

	if v.insecureSkipVerify, err = conf.FieldBool(vciFieldInsecureSkipVerify); err != nil {
		return nil, err
	}
	if v.objectTypes, err = conf.FieldStringList(vciFieldObjectTypes); err != nil {
		return nil, err
	}
	if len(v.objectTypes) == 0 {
		return nil, fmt.Errorf("at least one object type must be specified")
	}
	for _, t := range v.objectTypes {
		if _, ok := vcObjectTypes[t]; !ok {
			return nil, fmt.Errorf("invalid object type %q, must be one of %v", t, vcObjectTypeNames())
		}
	}
	if v.properties, err = conf.FieldStringList(vciFieldProperties); err != nil {
		return nil, err
	}
	if v.datacenter, err = conf.FieldString(vciFieldDatacenter); err != nil {
		return nil, err
	}
	if v.interval, err = conf.FieldDuration(vciFieldInterval); err != nil {
		return nil, err
	}

	return v, nil
}

func (v *vCenterInput) Connect(ctx context.Context) error {
	if v.client != nil {
		return nil
	}
	client, err := govmomi.NewClient(ctx, v.url, v.insecureSkipVerify)
	if err != nil {
		return fmt.Errorf("failed to connect to vCenter: %w", err)
	}
	v.client = client
	return nil
}

func (v *vCenterInput) Read(ctx context.Context) (*service.Message, service.AckFunc, error) {
	if v.client == nil {
		return nil, nil, service.ErrNotConnected
	}

	for len(v.pending) == 0 {
		if v.polled && v.interval <= 0 {
			return nil, nil, service.ErrEndOfInput
		}
		if wait := time.Until(v.nextPoll); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		// Schedule the next poll before collecting so that failures do not
		// cause a hot retry loop.
		v.nextPoll = time.Now().Add(v.interval)
		v.polled = true
		if err := v.poll(ctx); err != nil {
			// Force a fresh login on the next attempt, the session may have
			// expired.
			v.log.Errorf("Failed to collect from vCenter: %v", err)
			v.disconnect(ctx)
			return nil, nil, service.ErrNotConnected
		}
	}

	msg := v.pending[0]
	v.pending = v.pending[1:]
	return msg, func(ctx context.Context, err error) error {
		return nil
	}, nil
}

func (v *vCenterInput) poll(ctx context.Context) error {
	root, err := v.containerRoot(ctx)
	if err != nil {
		return err
	}

	manager := view.NewManager(v.client.Client)
	for _, objectType := range v.objectTypes {
		typeInfo := vcObjectTypes[objectType]

		props := typeInfo.defaultProps
		if len(v.properties) > 0 {
			props = ensureNameProp(v.properties)
		}

		containerView, err := manager.CreateContainerView(ctx, root, []string{typeInfo.kind}, true)
		if err != nil {
			return fmt.Errorf("failed to create view for %v: %w", objectType, err)
		}

		err = v.collectObjects(ctx, containerView, objectType, typeInfo.kind, props)
		if derr := containerView.Destroy(ctx); derr != nil {
			v.log.Warnf("Failed to destroy container view: %v", derr)
		}
		if err != nil {
			return fmt.Errorf("failed to retrieve %v objects: %w", objectType, err)
		}
	}
	return nil
}

func (v *vCenterInput) containerRoot(ctx context.Context) (types.ManagedObjectReference, error) {
	root := v.client.ServiceContent.RootFolder
	if v.datacenter == "" {
		return root, nil
	}
	finder := find.NewFinder(v.client.Client, false)
	dc, err := finder.Datacenter(ctx, v.datacenter)
	if err != nil {
		return root, fmt.Errorf("failed to find datacenter %q: %w", v.datacenter, err)
	}
	return dc.Reference(), nil
}

func (v *vCenterInput) collectObjects(ctx context.Context, containerView *view.ContainerView, objectType, kind string, props []string) error {
	var contents []types.ObjectContent
	if err := containerView.Retrieve(ctx, []string{kind}, props, &contents); err != nil {
		return err
	}

	for _, content := range contents {
		doc := map[string]any{
			"moRef": content.Obj.Value,
			"type":  content.Obj.Type,
		}
		var name string
		for _, prop := range content.PropSet {
			doc[prop.Name] = prop.Val
			if prop.Name == "name" {
				name, _ = prop.Val.(string)
			}
		}

		data, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("failed to marshal %v object: %w", objectType, err)
		}

		msg := service.NewMessage(data)
		msg.MetaSetMut("vcenter_object_type", objectType)
		msg.MetaSetMut("vcenter_moref", content.Obj.String())
		if name != "" {
			msg.MetaSetMut("vcenter_name", name)
		}
		v.pending = append(v.pending, msg)
	}
	return nil
}

func ensureNameProp(props []string) []string {
	for _, p := range props {
		if p == "name" {
			return props
		}
	}
	return append([]string{"name"}, props...)
}

func (v *vCenterInput) disconnect(ctx context.Context) {
	if v.client == nil {
		return
	}
	if err := v.client.Logout(ctx); err != nil {
		v.log.Debugf("Failed to log out of vCenter session: %v", err)
	}
	v.client = nil
}

func (v *vCenterInput) Close(ctx context.Context) error {
	v.disconnect(ctx)
	return nil
}
