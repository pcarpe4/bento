package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pcarpe4/bento/collector/core"
)

// redfishSource collects resources from BMCs (Dell iDRAC, HPE iLO, Lenovo
// XCC, ...) via the DMTF Redfish API using HTTP basic auth. Collection
// resources (containing a Members array) are expanded into one message per
// member.
type redfishSource struct {
	urls              []string
	endpoints         []string
	username          string
	password          string
	expandCollections bool
	client            *http.Client
}

func init() {
	core.RegisterSource("redfish", newRedfishSource)
}

func newRedfishSource(cfg core.Fields) (core.Source, error) {
	urls := cfg.StringList("urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("config field \"urls\" is required")
	}
	for i, u := range urls {
		urls[i] = strings.TrimSuffix(u, "/")
	}
	endpoints := cfg.StringList("endpoints")
	if len(endpoints) == 0 {
		endpoints = []string{"/redfish/v1/Systems"}
	}
	username, err := cfg.RequiredString("username")
	if err != nil {
		return nil, err
	}
	timeout, err := cfg.Duration("timeout", defaultHTTPTimeout)
	if err != nil {
		return nil, err
	}
	return &redfishSource{
		urls:              urls,
		endpoints:         endpoints,
		username:          username,
		password:          cfg.String("password", ""),
		expandCollections: cfg.Bool("expand_collections", true),
		client:            newHTTPClient(timeout, cfg.Bool("insecure_skip_verify", true)),
	}, nil
}

func (r *redfishSource) Collect(ctx context.Context) ([]core.Message, error) {
	var batch []core.Message
	for _, baseURL := range r.urls {
		for _, endpoint := range r.endpoints {
			doc, err := r.fetch(ctx, baseURL, endpoint)
			if err != nil {
				return nil, err
			}

			members := memberPaths(doc)
			if !r.expandCollections || members == nil {
				batch = append(batch, redfishMessage(baseURL, endpoint, doc))
				continue
			}
			for _, memberPath := range members {
				memberDoc, err := r.fetch(ctx, baseURL, memberPath)
				if err != nil {
					return nil, err
				}
				batch = append(batch, redfishMessage(baseURL, endpoint, memberDoc))
			}
		}
	}
	return batch, nil
}

func (r *redfishSource) fetch(ctx context.Context, baseURL, path string) (map[string]any, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(r.username, r.password)

	res, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request for %v%v failed: %w", baseURL, path, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response for %v%v: %w", baseURL, path, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("request for %v%v failed: status %v", baseURL, path, res.StatusCode)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse response for %v%v: %w", baseURL, path, err)
	}
	return doc, nil
}

// memberPaths returns referenced member paths if doc is a Redfish
// collection, otherwise nil.
func memberPaths(doc map[string]any) []string {
	members, ok := doc["Members"].([]any)
	if !ok {
		return nil
	}
	paths := make([]string, 0, len(members))
	for _, member := range members {
		if obj, ok := member.(map[string]any); ok {
			if id, ok := obj["@odata.id"].(string); ok && id != "" {
				paths = append(paths, id)
			}
		}
	}
	return paths
}

func redfishMessage(baseURL, endpoint string, doc map[string]any) core.Message {
	msg := core.NewMessage(doc)
	msg.Meta["url"] = baseURL
	msg.Meta["endpoint"] = endpoint
	if id, ok := doc["@odata.id"].(string); ok {
		msg.Meta["odata_id"] = id
	}
	if name, ok := doc["Name"].(string); ok {
		msg.Meta["name"] = name
	}
	return msg
}
