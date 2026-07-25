package redfish

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/warpstreamlabs/bento/public/service"
)

const (
	rfiFieldURLs               = "urls"
	rfiFieldUsername           = "username"
	rfiFieldPassword           = "password"
	rfiFieldAuthMode           = "auth_mode"
	rfiFieldEndpoints          = "endpoints"
	rfiFieldExpandCollections  = "expand_collections"
	rfiFieldInsecureSkipVerify = "insecure_skip_verify"
	rfiFieldInterval           = "interval"
	rfiFieldTimeout            = "timeout"
)

func redfishInputConfig() *service.ConfigSpec {
	return service.NewConfigSpec().
		Beta().
		Categories("Services").
		Version("1.20.0").
		Summary("Collects hardware inventory, health and telemetry from baseboard management controllers (Dell iDRAC, HPE iLO, Lenovo XCC, Supermicro and others) via the DMTF Redfish API.").
		Description(`
This input polls one or more Redfish service endpoints on an interval and
emits each retrieved resource as a JSON message. It is useful for feeding
asset inventories and monitoring pipelines with out-of-band server state
such as system health, thermal readings, power draw and firmware versions:

` + "```yaml" + `
input:
  redfish:
    urls: [ https://idrac-01.example.com, https://idrac-02.example.com ]
    username: svc-collector
    password: "${IDRAC_PASSWORD}"
    endpoints:
      - /redfish/v1/Systems
      - /redfish/v1/Chassis
    interval: 5m
` + "```" + `

When a fetched resource is a Redfish collection (it contains a ` + "`Members`" + `
array of references) and ` + "`expand_collections`" + ` is enabled, each member
resource is fetched and emitted as its own message instead of the collection
document. This means ` + "`/redfish/v1/Systems`" + ` yields one message per
system without having to know the vendor-specific member paths (such as
` + "`System.Embedded.1`" + ` on iDRAC) in advance.

By default the input authenticates with Redfish session tokens, creating a
session per host and renewing it automatically when it expires. Set
` + "`auth_mode: basic`" + ` to send HTTP basic auth credentials on every request
instead, which some older BMC firmwares require.

Management controllers commonly present self-signed TLS certificates, in
which case ` + "`insecure_skip_verify`" + ` must be enabled unless the
controller certificate is trusted by the host system.

Set ` + "`interval`" + ` to ` + "`0s`" + ` to collect exactly once and then shut the
input down, which is useful for batch-style jobs.

### Metadata

This input adds the following metadata fields to each message:

` + "```text" + `
- redfish_url
- redfish_endpoint
- redfish_odata_id
- redfish_odata_type
- redfish_id
- redfish_name
` + "```" + `

You can access these metadata fields using
[function interpolation](/docs/configuration/interpolation#bloblang-queries).
`).
		Field(service.NewStringListField(rfiFieldURLs).
			Description("Base URLs of the management controllers to collect from. All hosts are collected with the same credentials on every poll.").
			Example([]string{"https://idrac.example.com"}).
			Example([]string{"https://idrac-01.example.com", "https://idrac-02.example.com"})).
		Field(service.NewStringField(rfiFieldUsername).
			Description("The username to authenticate with.").
			Default("")).
		Field(service.NewStringField(rfiFieldPassword).
			Description("The password to authenticate with.").
			Default("").
			Secret()).
		Field(service.NewStringEnumField(rfiFieldAuthMode, "session", "basic").
			Description("How to authenticate against the Redfish service. `session` creates and renews a Redfish session token per host, `basic` sends HTTP basic auth credentials with every request.").
			Default("session").
			Advanced()).
		Field(service.NewStringListField(rfiFieldEndpoints).
			Description("Redfish resource paths to collect from each host.").
			Default([]any{"/redfish/v1/Systems"}).
			Example([]string{"/redfish/v1/Systems", "/redfish/v1/Chassis"}).
			Example([]string{"/redfish/v1/Chassis/System.Embedded.1/Thermal"}).
			Example([]string{"/redfish/v1/UpdateService/FirmwareInventory"})).
		Field(service.NewBoolField(rfiFieldExpandCollections).
			Description("Whether Redfish collection resources should be expanded by fetching each entry of their `Members` array and emitting those as individual messages.").
			Default(true)).
		Field(service.NewBoolField(rfiFieldInsecureSkipVerify).
			Description("Skip TLS certificate verification. Often required as management controllers typically present self-signed certificates.").
			Default(false).
			Advanced()).
		Field(service.NewDurationField(rfiFieldInterval).
			Description("The time between collections. Set to `0s` to collect exactly once and then shut down.").
			Default("5m")).
		Field(service.NewDurationField(rfiFieldTimeout).
			Description("Maximum duration of a single HTTP request.").
			Default("15s").
			Advanced())
}

func init() {
	err := service.RegisterInput(
		"redfish", redfishInputConfig(),
		func(conf *service.ParsedConfig, mgr *service.Resources) (service.Input, error) {
			return newRedfishInput(conf, mgr)
		})
	if err != nil {
		panic(err)
	}
}

//------------------------------------------------------------------------------

// redfishSession holds the state of an authenticated Redfish session on a
// single host.
type redfishSession struct {
	token    string
	location string
}

type redfishInput struct {
	log *service.Logger

	urls              []string
	username          string
	password          string
	sessionAuth       bool
	endpoints         []string
	expandCollections bool
	interval          time.Duration

	client   *http.Client
	sessions map[string]*redfishSession

	pending  []*service.Message
	nextPoll time.Time
	polled   bool
}

func newRedfishInput(conf *service.ParsedConfig, mgr *service.Resources) (*redfishInput, error) {
	r := &redfishInput{
		log:      mgr.Logger(),
		sessions: map[string]*redfishSession{},
	}

	var err error
	if r.urls, err = conf.FieldStringList(rfiFieldURLs); err != nil {
		return nil, err
	}
	if len(r.urls) == 0 {
		return nil, fmt.Errorf("at least one url must be specified")
	}
	for i, u := range r.urls {
		parsed, err := url.Parse(u)
		if err != nil {
			return nil, fmt.Errorf("failed to parse url %q: %w", u, err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("url %q must use http or https", u)
		}
		r.urls[i] = strings.TrimSuffix(u, "/")
	}

	if r.username, err = conf.FieldString(rfiFieldUsername); err != nil {
		return nil, err
	}
	if r.password, err = conf.FieldString(rfiFieldPassword); err != nil {
		return nil, err
	}
	authMode, err := conf.FieldString(rfiFieldAuthMode)
	if err != nil {
		return nil, err
	}
	r.sessionAuth = authMode == "session"

	if r.endpoints, err = conf.FieldStringList(rfiFieldEndpoints); err != nil {
		return nil, err
	}
	if len(r.endpoints) == 0 {
		return nil, fmt.Errorf("at least one endpoint must be specified")
	}
	if r.expandCollections, err = conf.FieldBool(rfiFieldExpandCollections); err != nil {
		return nil, err
	}
	if r.interval, err = conf.FieldDuration(rfiFieldInterval); err != nil {
		return nil, err
	}

	timeout, err := conf.FieldDuration(rfiFieldTimeout)
	if err != nil {
		return nil, err
	}
	insecure, err := conf.FieldBool(rfiFieldInsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	r.client = &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	return r, nil
}

func (r *redfishInput) Connect(ctx context.Context) error {
	if !r.sessionAuth {
		return nil
	}
	// Establish sessions eagerly so that authentication problems surface as
	// connection errors rather than mid-stream read failures.
	for _, baseURL := range r.urls {
		if r.sessions[baseURL] != nil {
			continue
		}
		if err := r.login(ctx, baseURL); err != nil {
			return err
		}
	}
	return nil
}

func (r *redfishInput) login(ctx context.Context, baseURL string) error {
	body, err := json.Marshal(map[string]string{
		"UserName": r.username,
		"Password": r.password,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/redfish/v1/SessionService/Sessions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create session on %v: %w", baseURL, err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("failed to create session on %v: status %v", baseURL, res.StatusCode)
	}

	token := res.Header.Get("X-Auth-Token")
	if token == "" {
		return fmt.Errorf("failed to create session on %v: response contained no X-Auth-Token header", baseURL)
	}

	r.sessions[baseURL] = &redfishSession{
		token:    token,
		location: res.Header.Get("Location"),
	}
	return nil
}

func (r *redfishInput) logout(ctx context.Context, baseURL string) {
	session := r.sessions[baseURL]
	if session == nil {
		return
	}
	delete(r.sessions, baseURL)

	if session.location == "" {
		return
	}
	location := session.location
	if strings.HasPrefix(location, "/") {
		location = baseURL + location
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, location, nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Auth-Token", session.token)
	res, err := r.client.Do(req)
	if err != nil {
		r.log.Debugf("Failed to delete redfish session on %v: %v", baseURL, err)
		return
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
}

// fetch retrieves a Redfish resource as a JSON document, re-authenticating
// once if an established session has expired.
func (r *redfishInput) fetch(ctx context.Context, baseURL, path string) (map[string]any, error) {
	doc, status, err := r.fetchOnce(ctx, baseURL, path)
	if err == nil && status == http.StatusUnauthorized && r.sessionAuth {
		r.log.Debugf("Session on %v expired, re-authenticating", baseURL)
		delete(r.sessions, baseURL)
		if err = r.login(ctx, baseURL); err != nil {
			return nil, err
		}
		doc, status, err = r.fetchOnce(ctx, baseURL, path)
	}
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("request for %v%v failed: status %v", baseURL, path, status)
	}
	return doc, nil
}

func (r *redfishInput) fetchOnce(ctx context.Context, baseURL, path string) (map[string]any, int, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")

	if session := r.sessions[baseURL]; session != nil {
		req.Header.Set("X-Auth-Token", session.token)
	} else if !r.sessionAuth && r.username != "" {
		req.SetBasicAuth(r.username, r.password)
	}

	res, err := r.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request for %v%v failed: %w", baseURL, path, err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, res.StatusCode, fmt.Errorf("failed to read response for %v%v: %w", baseURL, path, err)
	}

	var doc map[string]any
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, res.StatusCode, fmt.Errorf("failed to parse response for %v%v: %w", baseURL, path, err)
		}
	}
	return doc, res.StatusCode, nil
}

func (r *redfishInput) Read(ctx context.Context) (*service.Message, service.AckFunc, error) {
	for len(r.pending) == 0 {
		if r.polled && r.interval <= 0 {
			return nil, nil, service.ErrEndOfInput
		}
		if wait := time.Until(r.nextPoll); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		// Schedule the next poll before collecting so that failures do not
		// cause a hot retry loop.
		r.nextPoll = time.Now().Add(r.interval)
		r.polled = true
		if err := r.poll(ctx); err != nil {
			return nil, nil, err
		}
	}

	msg := r.pending[0]
	r.pending = r.pending[1:]
	return msg, func(ctx context.Context, err error) error {
		return nil
	}, nil
}

func (r *redfishInput) poll(ctx context.Context) error {
	for _, baseURL := range r.urls {
		for _, endpoint := range r.endpoints {
			doc, err := r.fetch(ctx, baseURL, endpoint)
			if err != nil {
				return err
			}

			memberPaths := collectionMemberPaths(doc)
			if !r.expandCollections || memberPaths == nil {
				r.pending = append(r.pending, redfishMessage(baseURL, endpoint, doc))
				continue
			}

			for _, memberPath := range memberPaths {
				memberDoc, err := r.fetch(ctx, baseURL, memberPath)
				if err != nil {
					return err
				}
				r.pending = append(r.pending, redfishMessage(baseURL, endpoint, memberDoc))
			}
		}
	}
	return nil
}

// collectionMemberPaths returns the referenced member paths if doc is a
// Redfish collection resource, otherwise nil.
func collectionMemberPaths(doc map[string]any) []string {
	members, ok := doc["Members"].([]any)
	if !ok {
		return nil
	}
	paths := make([]string, 0, len(members))
	for _, member := range members {
		memberObj, ok := member.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := memberObj["@odata.id"].(string); ok && id != "" {
			paths = append(paths, id)
		}
	}
	return paths
}

func redfishMessage(baseURL, endpoint string, doc map[string]any) *service.Message {
	msg := service.NewMessage(nil)
	msg.SetStructuredMut(doc)

	msg.MetaSetMut("redfish_url", baseURL)
	msg.MetaSetMut("redfish_endpoint", endpoint)
	for metaKey, docKey := range map[string]string{
		"redfish_odata_id":   "@odata.id",
		"redfish_odata_type": "@odata.type",
		"redfish_id":         "Id",
		"redfish_name":       "Name",
	} {
		if v, ok := doc[docKey].(string); ok && v != "" {
			msg.MetaSetMut(metaKey, v)
		}
	}
	return msg
}

func (r *redfishInput) Close(ctx context.Context) error {
	for baseURL := range r.sessions {
		r.logout(ctx, baseURL)
	}
	return nil
}
