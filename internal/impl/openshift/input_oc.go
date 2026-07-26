package openshift

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/warpstreamlabs/bento/public/service"
)

const (
	ociFieldBinaryPath         = "binary_path"
	ociFieldResources          = "resources"
	ociFieldArgs               = "args"
	ociFieldNamespace          = "namespace"
	ociFieldAllNamespaces      = "all_namespaces"
	ociFieldLabelSelector      = "label_selector"
	ociFieldFieldSelector      = "field_selector"
	ociFieldKubeconfig         = "kubeconfig"
	ociFieldContext            = "context"
	ociFieldServer             = "server"
	ociFieldToken              = "token"
	ociFieldCertAuthority      = "certificate_authority"
	ociFieldInsecureSkipVerify = "insecure_skip_verify"
	ociFieldInterval           = "interval"
	ociFieldTimeout            = "timeout"
	ociFieldSplitItems         = "split_items"
)

func openShiftOCInputConfig() *service.ConfigSpec {
	return service.NewConfigSpec().
		Beta().
		Categories("Services").
		Version("1.20.0").
		Summary("Collects resources from an OpenShift (or Kubernetes) cluster by periodically executing the `oc` command line tool.").
		Description(`
This input shells out to the ` + "`oc`" + ` binary on an interval and emits the
results as messages. There are two modes of operation:

### Resource collection mode

Set ` + "`resources`" + ` to a list of resource types and each poll executes
` + "`oc get <resource> -o json`" + ` for every entry. By default the returned
list is split so that each resource item becomes an individual message:

` + "```yaml" + `
input:
  openshift_oc:
    resources: [ pods, deploymentconfigs ]
    namespace: my-project
    interval: 1m
` + "```" + `

### Raw command mode

Alternatively, set ` + "`args`" + ` to run an arbitrary ` + "`oc`" + ` subcommand.
Standard output is emitted as a single message per execution. If the output
happens to be a JSON list object (` + "`kind: *List`" + `) and ` + "`split_items`" + `
is enabled it is split into individual messages:

` + "```yaml" + `
input:
  openshift_oc:
    args: [ "adm", "top", "nodes", "--no-headers" ]
    interval: 5m
` + "```" + `

Set ` + "`interval`" + ` to ` + "`0s`" + ` to collect exactly once and then shut the
input down, which is useful for batch-style jobs.

The subprocess inherits the environment of the Bento instance, so
authentication can also be provided via ` + "`$KUBECONFIG`" + ` or a logged-in
session. Note that credentials passed via the ` + "`token`" + ` field are supplied
to ` + "`oc`" + ` as a command line argument, which may be visible to other users
of the host via the process list.

### Metadata

In resource collection mode this input adds the following metadata fields to
each message:

` + "```text" + `
- openshift_resource
- openshift_resource_kind
- openshift_resource_name
- openshift_resource_namespace
- openshift_resource_uid
- openshift_resource_version
- openshift_resource_creation_timestamp
` + "```" + `

Additionally, all resource labels are added as metadata with the prefix
` + "`openshift_labels_`" + `, and all annotations are added with the prefix
` + "`openshift_annotations_`" + `. In raw command mode each message instead has an
` + "`openshift_oc_args`" + ` metadata field containing the executed arguments. You
can access these metadata fields using
[function interpolation](/docs/configuration/interpolation#bloblang-queries).
`).
		Field(service.NewStringListField(ociFieldResources).
			Description("A list of resource types to collect with `oc get <resource> -o json`. Each type is collected on every poll.").
			Default([]any{}).
			Example([]string{"pods"}).
			Example([]string{"deploymentconfigs", "routes", "imagestreams"})).
		Field(service.NewStringListField(ociFieldArgs).
			Description("Raw arguments passed to `oc` instead of resource collection. Mutually exclusive with `resources`.").
			Default([]any{}).
			Example([]string{"adm", "top", "nodes", "--no-headers"}).
			Advanced()).
		Field(service.NewStringField(ociFieldNamespace).
			Description("Namespace to collect resources from. When empty the namespace of the current `oc` context is used.").
			Default("")).
		Field(service.NewBoolField(ociFieldAllNamespaces).
			Description("Collect resources across all namespaces (`--all-namespaces`).").
			Default(false)).
		Field(service.NewStringMapField(ociFieldLabelSelector).
			Description("Label selector to filter resources.").
			Default(map[string]any{}).
			Example(map[string]any{"app": "myapp"})).
		Field(service.NewStringMapField(ociFieldFieldSelector).
			Description("Field selector to filter resources.").
			Default(map[string]any{}).
			Example(map[string]any{"status.phase": "Running"}).
			Advanced()).
		Field(service.NewStringField(ociFieldBinaryPath).
			Description("Path to the `oc` binary. Also compatible with `kubectl` for plain Kubernetes clusters.").
			Default("oc").
			Advanced()).
		Field(service.NewStringField(ociFieldKubeconfig).
			Description("Path to a kubeconfig file (`--kubeconfig`). When empty the default resolution of `oc` applies.").
			Default("")).
		Field(service.NewStringField(ociFieldContext).
			Description("Context within the kubeconfig to use (`--context`).").
			Default("")).
		Field(service.NewStringField(ociFieldServer).
			Description("Address of the API server (`--server`).").
			Default("").
			Advanced()).
		Field(service.NewStringField(ociFieldToken).
			Description("Bearer token for authentication (`--token`).").
			Default("").
			Secret().
			Advanced()).
		Field(service.NewStringField(ociFieldCertAuthority).
			Description("Path to a certificate authority file (`--certificate-authority`).").
			Default("").
			Advanced()).
		Field(service.NewBoolField(ociFieldInsecureSkipVerify).
			Description("Skip TLS certificate verification (`--insecure-skip-tls-verify`). Not recommended for production.").
			Default(false).
			Advanced()).
		Field(service.NewDurationField(ociFieldInterval).
			Description("The time between collections. Set to `0s` to collect exactly once and then shut down.").
			Default("30s")).
		Field(service.NewDurationField(ociFieldTimeout).
			Description("Maximum duration of a single `oc` execution.").
			Default("30s").
			Advanced()).
		Field(service.NewBoolField(ociFieldSplitItems).
			Description("Whether JSON list results should be split so that each item is emitted as an individual message.").
			Default(true)).
		LintRule(`
			let has_resources = this.resources.or([]).length() > 0
			let has_args = this.args.or([]).length() > 0
			root = if !$has_resources && !$has_args {
				"at least one of resources or args must be specified"
			} else if $has_resources && $has_args {
				"cannot specify both resources and args"
			}
		`)
}

func init() {
	err := service.RegisterInput(
		"openshift_oc", openShiftOCInputConfig(),
		func(conf *service.ParsedConfig, mgr *service.Resources) (service.Input, error) {
			return newOpenShiftOCInput(conf, mgr)
		})
	if err != nil {
		panic(err)
	}
}

//------------------------------------------------------------------------------

type openShiftOCInput struct {
	log *service.Logger

	binaryPath string
	resources  []string
	rawArgs    []string
	authFlags  []string
	queryFlags []string
	interval   time.Duration
	timeout    time.Duration
	splitItems bool

	pending  []*service.Message
	nextPoll time.Time
	polled   bool
}

func newOpenShiftOCInput(conf *service.ParsedConfig, mgr *service.Resources) (*openShiftOCInput, error) {
	o := &openShiftOCInput{
		log: mgr.Logger(),
	}

	var err error
	if o.binaryPath, err = conf.FieldString(ociFieldBinaryPath); err != nil {
		return nil, err
	}
	if o.resources, err = conf.FieldStringList(ociFieldResources); err != nil {
		return nil, err
	}
	if o.rawArgs, err = conf.FieldStringList(ociFieldArgs); err != nil {
		return nil, err
	}
	if len(o.resources) == 0 && len(o.rawArgs) == 0 {
		return nil, fmt.Errorf("at least one of %v or %v must be specified", ociFieldResources, ociFieldArgs)
	}
	if len(o.resources) > 0 && len(o.rawArgs) > 0 {
		return nil, fmt.Errorf("cannot specify both %v and %v", ociFieldResources, ociFieldArgs)
	}
	if o.interval, err = conf.FieldDuration(ociFieldInterval); err != nil {
		return nil, err
	}
	if o.timeout, err = conf.FieldDuration(ociFieldTimeout); err != nil {
		return nil, err
	}
	if o.splitItems, err = conf.FieldBool(ociFieldSplitItems); err != nil {
		return nil, err
	}

	if o.authFlags, err = ocAuthFlagsFromParsed(conf); err != nil {
		return nil, err
	}
	if o.queryFlags, err = ocQueryFlagsFromParsed(conf); err != nil {
		return nil, err
	}

	return o, nil
}

func ocAuthFlagsFromParsed(conf *service.ParsedConfig) ([]string, error) {
	var flags []string
	for field, flag := range map[string]string{
		ociFieldKubeconfig:    "--kubeconfig",
		ociFieldContext:       "--context",
		ociFieldServer:        "--server",
		ociFieldToken:         "--token",
		ociFieldCertAuthority: "--certificate-authority",
	} {
		v, err := conf.FieldString(field)
		if err != nil {
			return nil, err
		}
		if v != "" {
			flags = append(flags, flag+"="+v)
		}
	}
	insecure, err := conf.FieldBool(ociFieldInsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	if insecure {
		flags = append(flags, "--insecure-skip-tls-verify=true")
	}
	sort.Strings(flags)
	return flags, nil
}

func ocQueryFlagsFromParsed(conf *service.ParsedConfig) ([]string, error) {
	var flags []string

	namespace, err := conf.FieldString(ociFieldNamespace)
	if err != nil {
		return nil, err
	}
	if namespace != "" {
		flags = append(flags, "--namespace="+namespace)
	}

	allNamespaces, err := conf.FieldBool(ociFieldAllNamespaces)
	if err != nil {
		return nil, err
	}
	if allNamespaces {
		flags = append(flags, "--all-namespaces")
	}

	labelSelector, err := conf.FieldStringMap(ociFieldLabelSelector)
	if err != nil {
		return nil, err
	}
	if s := selectorFromMap(labelSelector); s != "" {
		flags = append(flags, "--selector="+s)
	}

	fieldSelector, err := conf.FieldStringMap(ociFieldFieldSelector)
	if err != nil {
		return nil, err
	}
	if s := selectorFromMap(fieldSelector); s != "" {
		flags = append(flags, "--field-selector="+s)
	}

	return flags, nil
}

func selectorFromMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (o *openShiftOCInput) Connect(ctx context.Context) error {
	if _, err := exec.LookPath(o.binaryPath); err != nil {
		return fmt.Errorf("unable to find binary %q: %w", o.binaryPath, err)
	}
	return nil
}

func (o *openShiftOCInput) Read(ctx context.Context) (*service.Message, service.AckFunc, error) {
	for len(o.pending) == 0 {
		if o.polled && o.interval <= 0 {
			return nil, nil, service.ErrEndOfInput
		}
		if wait := time.Until(o.nextPoll); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		// Schedule the next poll before collecting so that failures do not
		// cause a hot retry loop.
		o.nextPoll = time.Now().Add(o.interval)
		o.polled = true
		if err := o.poll(ctx); err != nil {
			return nil, nil, err
		}
	}

	msg := o.pending[0]
	o.pending = o.pending[1:]
	return msg, func(ctx context.Context, err error) error {
		return nil
	}, nil
}

func (o *openShiftOCInput) poll(ctx context.Context) error {
	if len(o.rawArgs) > 0 {
		args := append(append([]string{}, o.rawArgs...), o.authFlags...)
		stdout, err := o.runOC(ctx, args)
		if err != nil {
			return err
		}
		o.pending = append(o.pending, o.outputToMessages("", args, stdout)...)
		return nil
	}

	for _, resource := range o.resources {
		args := []string{"get", resource, "--output=json"}
		args = append(args, o.queryFlags...)
		args = append(args, o.authFlags...)
		stdout, err := o.runOC(ctx, args)
		if err != nil {
			return err
		}
		msgs := o.outputToMessages(resource, args, stdout)
		o.log.Debugf("Collected %v messages for resource %v", len(msgs), resource)
		o.pending = append(o.pending, msgs...)
	}
	return nil
}

func (o *openShiftOCInput) runOC(ctx context.Context, args []string) ([]byte, error) {
	cmdCtx := ctx
	if o.timeout > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(cmdCtx, o.binaryPath, args...)
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("%v %v failed: %w: %v", o.binaryPath, redactArgs(args), err, detail)
		}
		return nil, fmt.Errorf("%v %v failed: %w", o.binaryPath, redactArgs(args), err)
	}
	return stdout.Bytes(), nil
}

// redactArgs hides token values from error messages.
func redactArgs(args []string) []string {
	redacted := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "--token=") {
			a = "--token=!!!SECRET_SCRUBBED!!!"
		}
		redacted[i] = a
	}
	return redacted
}

func (o *openShiftOCInput) outputToMessages(resource string, args []string, stdout []byte) []*service.Message {
	var doc map[string]any
	if err := json.Unmarshal(stdout, &doc); err != nil {
		// Non-JSON output (e.g. `oc adm top`) is emitted verbatim.
		msg := service.NewMessage(bytes.TrimSpace(stdout))
		annotateOCMessage(msg, resource, args, nil)
		return []*service.Message{msg}
	}

	kind, _ := doc["kind"].(string)
	items, hasItems := doc["items"].([]any)
	if !o.splitItems || !strings.HasSuffix(kind, "List") || !hasItems {
		msg := service.NewMessage(nil)
		msg.SetStructuredMut(doc)
		annotateOCMessage(msg, resource, args, doc)
		return []*service.Message{msg}
	}

	msgs := make([]*service.Message, 0, len(items))
	for _, item := range items {
		itemObj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		msg := service.NewMessage(nil)
		msg.SetStructuredMut(itemObj)
		annotateOCMessage(msg, resource, args, itemObj)
		msgs = append(msgs, msg)
	}
	return msgs
}

func annotateOCMessage(msg *service.Message, resource string, args []string, obj map[string]any) {
	if resource != "" {
		msg.MetaSetMut("openshift_resource", resource)
	} else {
		msg.MetaSetMut("openshift_oc_args", strings.Join(redactArgs(args), " "))
	}
	if obj == nil {
		return
	}

	if kind, ok := obj["kind"].(string); ok && kind != "" {
		msg.MetaSetMut("openshift_resource_kind", kind)
	}
	metadata, _ := obj["metadata"].(map[string]any)
	if metadata == nil {
		return
	}
	for metaKey, docKey := range map[string]string{
		"openshift_resource_name":               "name",
		"openshift_resource_namespace":          "namespace",
		"openshift_resource_uid":                "uid",
		"openshift_resource_version":            "resourceVersion",
		"openshift_resource_creation_timestamp": "creationTimestamp",
	} {
		if v, ok := metadata[docKey].(string); ok && v != "" {
			msg.MetaSetMut(metaKey, v)
		}
	}
	if labels, ok := metadata["labels"].(map[string]any); ok {
		for k, v := range labels {
			if s, ok := v.(string); ok {
				msg.MetaSetMut("openshift_labels_"+k, s)
			}
		}
	}
	if annotations, ok := metadata["annotations"].(map[string]any); ok {
		for k, v := range annotations {
			if s, ok := v.(string); ok {
				msg.MetaSetMut("openshift_annotations_"+k, s)
			}
		}
	}
}

func (o *openShiftOCInput) Close(ctx context.Context) error {
	return nil
}
