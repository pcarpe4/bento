package sources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pcarpe4/bento/collector/core"
)

func TestHTTPAPISource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if user, pass, _ := req.BasicAuth(); user != "u" || pass != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if req.Header.Get("X-Custom") != "yes" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{
			map[string]any{"id": "a"},
			map[string]any{"id": "b"},
		})
	}))
	defer server.Close()

	src, err := newHTTPAPISource(core.Fields{
		"urls":     server.URL,
		"username": "u",
		"password": "p",
		"headers":  map[string]any{"X-Custom": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}

	batch, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(batch))
	}
	if batch[0].Data["id"] != "a" || batch[0].Meta["url"] != server.URL {
		t.Errorf("unexpected message: %+v", batch[0])
	}
}

func TestHTTPAPISourceErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	src, err := newHTTPAPISource(core.Fields{"urls": server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Collect(context.Background()); err == nil {
		t.Error("expected an error for status 403")
	}
}

func TestRedfishSourceExpandsCollections(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /redfish/v1/Systems", func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Members": []any{map[string]any{"@odata.id": "/redfish/v1/Systems/S1"}},
		})
	})
	mux.HandleFunc("GET /redfish/v1/Systems/S1", func(w http.ResponseWriter, req *http.Request) {
		if user, pass, _ := req.BasicAuth(); user != "u" || pass != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"@odata.id": "/redfish/v1/Systems/S1",
			"Name":      "System",
			"Model":     "PowerEdge R750",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	src, err := newRedfishSource(core.Fields{
		"urls":     server.URL,
		"username": "u",
		"password": "p",
	})
	if err != nil {
		t.Fatal(err)
	}

	batch, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 {
		t.Fatalf("expected 1 message, got %d", len(batch))
	}
	if batch[0].Data["Model"] != "PowerEdge R750" {
		t.Errorf("unexpected data: %v", batch[0].Data)
	}
	if batch[0].Meta["odata_id"] != "/redfish/v1/Systems/S1" {
		t.Errorf("unexpected meta: %v", batch[0].Meta)
	}
}

func TestSourceConfigValidation(t *testing.T) {
	if _, err := newHTTPAPISource(core.Fields{}); err == nil {
		t.Error("http_api: expected error without urls")
	}
	if _, err := newRedfishSource(core.Fields{"urls": "https://x"}); err == nil {
		t.Error("redfish: expected error without username")
	}
	if _, err := newOCSource(core.Fields{}); err == nil {
		t.Error("oc: expected error without resources")
	}
	if _, err := newVCenterSource(core.Fields{"url": "https://x/sdk", "object_types": []any{"nope"}}); err == nil {
		t.Error("vcenter: expected error for invalid object type")
	}
}
