package destinations

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pcarpe4/bento/collector/core"
)

// httpPostDestination POSTs each batch as a JSON array to a URL.
type httpPostDestination struct {
	url      string
	headers  map[string]string
	username string
	password string
	client   *http.Client
}

func init() {
	core.RegisterDestination("http_post", newHTTPPostDestination)
}

func newHTTPPostDestination(cfg core.Fields) (core.Destination, error) {
	u, err := cfg.RequiredString("url")
	if err != nil {
		return nil, err
	}
	timeout, err := cfg.Duration("timeout", 15*time.Second)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Bool("insecure_skip_verify", false) {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &httpPostDestination{
		url:      u,
		headers:  cfg.StringMap("headers"),
		username: cfg.String("username", ""),
		password: cfg.String("password", ""),
		client:   &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

func (h *httpPostDestination) Write(ctx context.Context, batch []core.Message) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	if h.username != "" {
		req.SetBasicAuth(h.username, h.password)
	}

	res, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("post to %v failed: %w", h.url, err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("post to %v failed: status %v", h.url, res.StatusCode)
	}
	return nil
}

func (h *httpPostDestination) Close(ctx context.Context) error {
	return nil
}
