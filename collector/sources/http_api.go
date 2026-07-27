// Package sources contains the built-in source types. Adding a new source
// type means adding one file to this package: implement core.Source and
// register a constructor in an init function.
package sources

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pcarpe4/bento/collector/core"
)

// httpAPISource polls one or more JSON HTTP endpoints.
type httpAPISource struct {
	urls     []string
	headers  map[string]string
	username string
	password string
	client   *http.Client
}

func init() {
	core.RegisterSource("http_api", newHTTPAPISource)
}

func newHTTPAPISource(cfg core.Fields) (core.Source, error) {
	urls := cfg.StringList("urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("config field \"urls\" is required")
	}
	timeout, err := cfg.Duration("timeout", defaultHTTPTimeout)
	if err != nil {
		return nil, err
	}
	return &httpAPISource{
		urls:     urls,
		headers:  cfg.StringMap("headers"),
		username: cfg.String("username", ""),
		password: cfg.String("password", ""),
		client:   newHTTPClient(timeout, cfg.Bool("insecure_skip_verify", false)),
	}, nil
}

func (h *httpAPISource) Collect(ctx context.Context) ([]core.Message, error) {
	var batch []core.Message
	for _, u := range h.urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		for k, v := range h.headers {
			req.Header.Set(k, v)
		}
		if h.username != "" {
			req.SetBasicAuth(h.username, h.password)
		}

		res, err := h.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request for %v failed: %w", u, err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response from %v: %w", u, err)
		}
		if res.StatusCode < 200 || res.StatusCode > 299 {
			return nil, fmt.Errorf("request for %v failed: status %v", u, res.StatusCode)
		}

		for _, msg := range jsonToMessages(body) {
			msg.Meta["url"] = u
			batch = append(batch, msg)
		}
	}
	return batch, nil
}

// jsonToMessages converts a response body into messages: a JSON array yields
// one message per object element, a JSON object yields a single message, and
// anything else is wrapped as {"raw": "..."}.
func jsonToMessages(body []byte) []core.Message {
	var asObject map[string]any
	if err := json.Unmarshal(body, &asObject); err == nil {
		return []core.Message{core.NewMessage(asObject)}
	}
	var asList []any
	if err := json.Unmarshal(body, &asList); err == nil {
		msgs := make([]core.Message, 0, len(asList))
		for _, item := range asList {
			if obj, ok := item.(map[string]any); ok {
				msgs = append(msgs, core.NewMessage(obj))
			} else {
				msgs = append(msgs, core.NewMessage(map[string]any{"value": item}))
			}
		}
		return msgs
	}
	return []core.Message{core.NewMessage(map[string]any{"raw": strings.TrimSpace(string(body))})}
}

const defaultHTTPTimeout = 15 * time.Second

func newHTTPClient(timeout time.Duration, insecure bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
