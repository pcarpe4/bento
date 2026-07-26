package redfish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/warpstreamlabs/bento/public/service"
)

// fakeRedfish is a minimal Redfish service used for testing, implementing
// session auth, a systems collection and a thermal resource.
type fakeRedfish struct {
	mu            sync.Mutex
	username      string
	password      string
	tokens        map[string]bool
	loginCount    int
	sessionsMade  int
	deletedTokens int
}

func newFakeRedfish() *fakeRedfish {
	return &fakeRedfish{
		username: "svc-collector",
		password: "hunter2",
		tokens:   map[string]bool{},
	}
}

func (f *fakeRedfish) authorized(req *http.Request) bool {
	if token := req.Header.Get("X-Auth-Token"); token != "" {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.tokens[token]
	}
	if user, pass, ok := req.BasicAuth(); ok {
		return user == f.username && pass == f.password
	}
	return false
}

// expireSessions invalidates all issued tokens, simulating BMC session
// timeouts.
func (f *fakeRedfish) expireSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = map[string]bool{}
}

func (f *fakeRedfish) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /redfish/v1/SessionService/Sessions", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.loginCount++
		if body["UserName"] != f.username || body["Password"] != f.password {
			f.mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.sessionsMade++
		token := fmt.Sprintf("token-%v", f.sessionsMade)
		f.tokens[token] = true
		f.mu.Unlock()

		w.Header().Set("X-Auth-Token", token)
		w.Header().Set("Location", "/redfish/v1/SessionService/Sessions/"+token)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": token})
	})

	mux.HandleFunc("DELETE /redfish/v1/SessionService/Sessions/{id}", func(w http.ResponseWriter, req *http.Request) {
		f.mu.Lock()
		delete(f.tokens, req.PathValue("id"))
		f.deletedTokens++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	serveJSON := func(w http.ResponseWriter, req *http.Request, doc map[string]any) {
		if !f.authorized(req) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}

	mux.HandleFunc("GET /redfish/v1/Systems", func(w http.ResponseWriter, req *http.Request) {
		serveJSON(w, req, map[string]any{
			"@odata.id":   "/redfish/v1/Systems",
			"@odata.type": "#ComputerSystemCollection.ComputerSystemCollection",
			"Name":        "Computer System Collection",
			"Members": []any{
				map[string]any{"@odata.id": "/redfish/v1/Systems/System.Embedded.1"},
			},
			"Members@odata.count": 1,
		})
	})

	mux.HandleFunc("GET /redfish/v1/Systems/System.Embedded.1", func(w http.ResponseWriter, req *http.Request) {
		serveJSON(w, req, map[string]any{
			"@odata.id":    "/redfish/v1/Systems/System.Embedded.1",
			"@odata.type":  "#ComputerSystem.v1_16_0.ComputerSystem",
			"Id":           "System.Embedded.1",
			"Name":         "System",
			"Manufacturer": "Dell Inc.",
			"Model":        "PowerEdge R750",
			"PowerState":   "On",
			"Status":       map[string]any{"Health": "OK", "State": "Enabled"},
		})
	})

	mux.HandleFunc("GET /redfish/v1/Chassis/System.Embedded.1/Thermal", func(w http.ResponseWriter, req *http.Request) {
		serveJSON(w, req, map[string]any{
			"@odata.id":   "/redfish/v1/Chassis/System.Embedded.1/Thermal",
			"@odata.type": "#Thermal.v1_7_0.Thermal",
			"Id":          "Thermal",
			"Name":        "Thermal",
			"Temperatures": []any{
				map[string]any{"Name": "CPU1 Temp", "ReadingCelsius": 42},
			},
		})
	})

	return mux
}

func newRedfishTestInput(t *testing.T, configYAML string) *redfishInput {
	t.Helper()
	spec := redfishInputConfig()
	conf, err := spec.ParseYAML(configYAML, nil)
	require.NoError(t, err)
	in, err := newRedfishInput(conf, service.MockResources())
	require.NoError(t, err)
	return in
}

func readAll(t *testing.T, ctx context.Context, in *redfishInput) []*service.Message {
	t.Helper()
	var msgs []*service.Message
	for {
		msg, ack, err := in.Read(ctx)
		if err != nil {
			assert.ErrorIs(t, err, service.ErrEndOfInput)
			return msgs
		}
		require.NotNil(t, ack)
		msgs = append(msgs, msg)
	}
}

func TestRedfishConfigParse(t *testing.T) {
	spec := redfishInputConfig()

	for name, config := range map[string]string{
		"minimal": `
urls: [ https://idrac.example.com ]
`,
		"full": `
urls: [ https://idrac-01.example.com, https://idrac-02.example.com ]
username: svc-collector
password: hunter2
auth_mode: basic
endpoints: [ /redfish/v1/Systems, /redfish/v1/Chassis ]
expand_collections: false
insecure_skip_verify: true
interval: 1m
timeout: 5s
`,
	} {
		t.Run(name, func(t *testing.T) {
			conf, err := spec.ParseYAML(config, nil)
			require.NoError(t, err)
			require.NotNil(t, conf)
		})
	}
}

func TestRedfishConfigValidation(t *testing.T) {
	conf, err := redfishInputConfig().ParseYAML(`urls: []`, nil)
	require.NoError(t, err)
	_, err = newRedfishInput(conf, service.MockResources())
	require.Error(t, err)

	conf, err = redfishInputConfig().ParseYAML(`urls: [ "ftp://nope.example.com" ]`, nil)
	require.NoError(t, err)
	_, err = newRedfishInput(conf, service.MockResources())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http or https")
}

func TestRedfishSessionCollectAndExpand(t *testing.T) {
	fake := newFakeRedfish()
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	in := newRedfishTestInput(t, fmt.Sprintf(`
urls: [ %v ]
username: svc-collector
password: hunter2
endpoints:
  - /redfish/v1/Systems
  - /redfish/v1/Chassis/System.Embedded.1/Thermal
interval: 0s
`, server.URL))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msgs := readAll(t, ctx, in)
	require.Len(t, msgs, 2)

	// The systems collection is expanded into its single member.
	structured, err := msgs[0].AsStructured()
	require.NoError(t, err)
	assert.Equal(t, "PowerEdge R750", structured.(map[string]any)["Model"])

	meta := func(m *service.Message, key string) string {
		v, _ := m.MetaGet(key)
		return v
	}
	assert.Equal(t, server.URL, meta(msgs[0], "redfish_url"))
	assert.Equal(t, "/redfish/v1/Systems", meta(msgs[0], "redfish_endpoint"))
	assert.Equal(t, "/redfish/v1/Systems/System.Embedded.1", meta(msgs[0], "redfish_odata_id"))
	assert.Equal(t, "System.Embedded.1", meta(msgs[0], "redfish_id"))
	assert.Equal(t, "System", meta(msgs[0], "redfish_name"))

	// The thermal resource is not a collection and is emitted as-is.
	assert.Equal(t, "Thermal", meta(msgs[1], "redfish_id"))

	require.NoError(t, in.Close(ctx))
	assert.Equal(t, 1, fake.sessionsMade)
	assert.Equal(t, 1, fake.deletedTokens, "session should be deleted on close")
}

func TestRedfishNoExpand(t *testing.T) {
	fake := newFakeRedfish()
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	in := newRedfishTestInput(t, fmt.Sprintf(`
urls: [ %v ]
username: svc-collector
password: hunter2
endpoints: [ /redfish/v1/Systems ]
expand_collections: false
interval: 0s
`, server.URL))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msgs := readAll(t, ctx, in)
	require.Len(t, msgs, 1)

	structured, err := msgs[0].AsStructured()
	require.NoError(t, err)
	assert.Equal(t, "Computer System Collection", structured.(map[string]any)["Name"])
}

func TestRedfishBasicAuth(t *testing.T) {
	fake := newFakeRedfish()
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	in := newRedfishTestInput(t, fmt.Sprintf(`
urls: [ %v ]
username: svc-collector
password: hunter2
auth_mode: basic
endpoints: [ /redfish/v1/Systems ]
interval: 0s
`, server.URL))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msgs := readAll(t, ctx, in)
	require.Len(t, msgs, 1)
	assert.Equal(t, 0, fake.sessionsMade, "basic auth must not create sessions")
}

func TestRedfishSessionRenewal(t *testing.T) {
	fake := newFakeRedfish()
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	in := newRedfishTestInput(t, fmt.Sprintf(`
urls: [ %v ]
username: svc-collector
password: hunter2
endpoints: [ /redfish/v1/Chassis/System.Embedded.1/Thermal ]
interval: 1ms
`, server.URL))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	_, _, err := in.Read(ctx)
	require.NoError(t, err)

	// Simulate the BMC expiring the session, the next poll must transparently
	// re-authenticate.
	fake.expireSessions()

	_, _, err = in.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.sessionsMade, "expected a second session after expiry")

	require.NoError(t, in.Close(ctx))
}

func TestRedfishBadCredentials(t *testing.T) {
	fake := newFakeRedfish()
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	in := newRedfishTestInput(t, fmt.Sprintf(`
urls: [ %v ]
username: svc-collector
password: wrong
endpoints: [ /redfish/v1/Systems ]
interval: 0s
`, server.URL))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.Error(t, in.Connect(ctx))
}

func TestRedfishMultiHost(t *testing.T) {
	fakeA, fakeB := newFakeRedfish(), newFakeRedfish()
	serverA := httptest.NewServer(fakeA.handler())
	defer serverA.Close()
	serverB := httptest.NewServer(fakeB.handler())
	defer serverB.Close()

	in := newRedfishTestInput(t, fmt.Sprintf(`
urls: [ %v, %v ]
username: svc-collector
password: hunter2
endpoints: [ /redfish/v1/Systems ]
interval: 0s
`, serverA.URL, serverB.URL))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, in.Connect(ctx))

	msgs := readAll(t, ctx, in)
	require.Len(t, msgs, 2)

	hosts := map[string]bool{}
	for _, msg := range msgs {
		v, ok := msg.MetaGet("redfish_url")
		require.True(t, ok)
		hosts[v] = true
	}
	assert.Len(t, hosts, 2, "expected one message per host")
	assert.Equal(t, 1, fakeA.sessionsMade)
	assert.Equal(t, 1, fakeB.sessionsMade)
}
