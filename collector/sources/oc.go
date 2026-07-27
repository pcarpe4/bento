package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pcarpe4/bento/collector/core"
)

// ocSource collects resources from an OpenShift or Kubernetes cluster by
// executing `oc get <resource> -o json` (works with kubectl too).
type ocSource struct {
	binaryPath string
	resources  []string
	flags      []string
	timeout    time.Duration
}

func init() {
	core.RegisterSource("oc", newOCSource)
}

func newOCSource(cfg core.Fields) (core.Source, error) {
	resources := cfg.StringList("resources")
	if len(resources) == 0 {
		return nil, fmt.Errorf("config field \"resources\" is required")
	}

	var flags []string
	for field, flag := range map[string]string{
		"namespace":  "--namespace",
		"kubeconfig": "--kubeconfig",
		"context":    "--context",
		"server":     "--server",
		"token":      "--token",
	} {
		if v := cfg.String(field, ""); v != "" {
			flags = append(flags, flag+"="+v)
		}
	}
	if cfg.Bool("all_namespaces", false) {
		flags = append(flags, "--all-namespaces")
	}
	if cfg.Bool("insecure_skip_verify", false) {
		flags = append(flags, "--insecure-skip-tls-verify=true")
	}
	if selector := cfg.String("label_selector", ""); selector != "" {
		flags = append(flags, "--selector="+selector)
	}

	timeout, err := cfg.Duration("timeout", 30*time.Second)
	if err != nil {
		return nil, err
	}

	binaryPath := cfg.String("binary_path", "oc")
	if _, err := exec.LookPath(binaryPath); err != nil {
		return nil, fmt.Errorf("unable to find binary %q: %w", binaryPath, err)
	}

	return &ocSource{
		binaryPath: binaryPath,
		resources:  resources,
		flags:      flags,
		timeout:    timeout,
	}, nil
}

func (o *ocSource) Collect(ctx context.Context) ([]core.Message, error) {
	var batch []core.Message
	for _, resource := range o.resources {
		args := append([]string{"get", resource, "--output=json"}, o.flags...)

		cmdCtx, cancel := context.WithTimeout(ctx, o.timeout)
		cmd := exec.CommandContext(cmdCtx, o.binaryPath, args...)
		cmd.Env = os.Environ()
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			detail := strings.TrimSpace(stderr.String())
			return nil, fmt.Errorf("%v get %v failed: %w: %v", o.binaryPath, resource, err, detail)
		}

		var doc map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
			return nil, fmt.Errorf("failed to parse %v get %v output: %w", o.binaryPath, resource, err)
		}

		items, ok := doc["items"].([]any)
		if !ok {
			batch = append(batch, ocMessage(resource, doc))
			continue
		}
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				batch = append(batch, ocMessage(resource, obj))
			}
		}
	}
	return batch, nil
}

func ocMessage(resource string, doc map[string]any) core.Message {
	msg := core.NewMessage(doc)
	msg.Meta["resource"] = resource
	if kind, ok := doc["kind"].(string); ok {
		msg.Meta["kind"] = kind
	}
	if metadata, ok := doc["metadata"].(map[string]any); ok {
		if name, ok := metadata["name"].(string); ok {
			msg.Meta["name"] = name
		}
		if namespace, ok := metadata["namespace"].(string); ok {
			msg.Meta["namespace"] = namespace
		}
	}
	return msg
}
