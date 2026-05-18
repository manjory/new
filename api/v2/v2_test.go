package v2_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rainbowmga/timetravel/api"
	apiv2 "github.com/rainbowmga/timetravel/api/v2"
	"github.com/rainbowmga/timetravel/service"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	svc, err := service.NewSQLiteRecordService(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	router := mux.NewRouter()

	v1 := api.NewAPI(svc)
	v1Route := router.PathPrefix("/api/v1").Subrouter()
	v1.CreateRoutes(v1Route)

	v2 := apiv2.NewAPI(svc)
	v2Route := router.PathPrefix("/api/v2").Subrouter()
	v2.CreateRoutes(v2Route)

	return httptest.NewServer(router)
}

func do(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestE2E_V1AndV2(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// --- v1: create record 1 ---
	status, body := do(t, "POST", srv.URL+"/api/v1/records/1",
		`{"hello":"world"}`)
	if status != 200 {
		t.Fatalf("v1 create status %d body %s", status, body)
	}
	// v1 response shape MUST stay the same -- no version/created_at keys.
	if strings.Contains(body, "version") || strings.Contains(body, "created_at") {
		t.Fatalf("v1 response leaked v2 fields: %s", body)
	}

	// --- v1: update twice ---
	if status, body := do(t, "POST", srv.URL+"/api/v1/records/1",
		`{"hello":"world 2","status":"ok"}`); status != 200 {
		t.Fatalf("v1 update status %d body %s", status, body)
	}
	if status, body := do(t, "POST", srv.URL+"/api/v1/records/1",
		`{"hello":null}`); status != 200 {
		t.Fatalf("v1 delete-field status %d body %s", status, body)
	}

	// --- v2: list versions, expect 3 ---
	status, body = do(t, "GET", srv.URL+"/api/v2/records/1/versions", "")
	if status != 200 {
		t.Fatalf("v2 list status %d body %s", status, body)
	}
	var listResp struct {
		ID       int `json:"id"`
		Versions []struct {
			Version    int    `json:"version"`
			CreatedAt  string `json:"created_at"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(body), &listResp); err != nil {
		t.Fatalf("list decode: %v -- body=%s", err, body)
	}
	if listResp.ID != 1 || len(listResp.Versions) != 3 {
		t.Fatalf("expected id=1 with 3 versions, got %+v", listResp)
	}
	for i, v := range listResp.Versions {
		if v.Version != i+1 || v.CreatedAt == "" {
			t.Fatalf("bad version entry %d: %+v", i, v)
		}
	}

	// --- v2: fetch latest -- should have version=3, hello deleted ---
	status, body = do(t, "GET", srv.URL+"/api/v2/records/1", "")
	if status != 200 {
		t.Fatalf("v2 latest status %d body %s", status, body)
	}
	var latest struct {
		ID        int               `json:"id"`
		Version   int               `json:"version"`
		CreatedAt string            `json:"created_at"`
		Data      map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &latest); err != nil {
		t.Fatalf("latest decode: %v", err)
	}
	if latest.ID != 1 || latest.Version != 3 {
		t.Fatalf("expected id=1 version=3, got %+v", latest)
	}
	if _, has := latest.Data["hello"]; has {
		t.Fatalf("hello should be absent in latest: %+v", latest.Data)
	}
	if latest.Data["status"] != "ok" {
		t.Fatalf("status should be 'ok' in latest: %+v", latest.Data)
	}

	// --- v2: fetch version 1 -- {hello: "world"} ---
	status, body = do(t, "GET", srv.URL+"/api/v2/records/1/versions/1", "")
	if status != 200 {
		t.Fatalf("v2 v1 status %d body %s", status, body)
	}
	var v1Snap struct {
		Version int               `json:"version"`
		Data    map[string]string `json:"data"`
	}
	json.Unmarshal([]byte(body), &v1Snap)
	if v1Snap.Version != 1 || v1Snap.Data["hello"] != "world" || len(v1Snap.Data) != 1 {
		t.Fatalf("unexpected v1 snapshot: %s", body)
	}

	// --- v2: POST should create a 4th version and return version metadata ---
	status, body = do(t, "POST", srv.URL+"/api/v2/records/1",
		`{"new":"value"}`)
	if status != 200 {
		t.Fatalf("v2 post status %d body %s", status, body)
	}
	var v2Post struct {
		Version int               `json:"version"`
		Data    map[string]string `json:"data"`
	}
	json.Unmarshal([]byte(body), &v2Post)
	if v2Post.Version != 4 {
		t.Fatalf("expected version=4, got %+v body=%s", v2Post, body)
	}
	if v2Post.Data["new"] != "value" || v2Post.Data["status"] != "ok" {
		t.Fatalf("expected merged data, got %+v", v2Post.Data)
	}

	// --- v2: POST on a brand-new id should create record + version 1 ---
	status, body = do(t, "POST", srv.URL+"/api/v2/records/77",
		`{"x":"y"}`)
	if status != 200 {
		t.Fatalf("v2 new-record post status %d body %s", status, body)
	}
	var fresh struct {
		Version int               `json:"version"`
		Data    map[string]string `json:"data"`
	}
	json.Unmarshal([]byte(body), &fresh)
	if fresh.Version != 1 || fresh.Data["x"] != "y" {
		t.Fatalf("expected version=1 x=y, got %+v body=%s", fresh, body)
	}

	// --- error cases ---

	// missing record on v2 latest -> 404
	if status, _ := do(t, "GET", srv.URL+"/api/v2/records/9999", ""); status != 404 {
		t.Fatalf("expected 404 for missing record, got %d", status)
	}
	// missing version -> 404
	if status, _ := do(t, "GET", srv.URL+"/api/v2/records/1/versions/999", ""); status != 404 {
		t.Fatalf("expected 404 for missing version, got %d", status)
	}
	// invalid id -> 400
	if status, _ := do(t, "GET", srv.URL+"/api/v2/records/abc", ""); status != 400 {
		t.Fatalf("expected 400 for invalid id, got %d", status)
	}

	// v1 BACKWARD COMPAT: v1 GET still works and still has no version field
	status, body = do(t, "GET", srv.URL+"/api/v1/records/1", "")
	if status != 200 {
		t.Fatalf("v1 get status %d body %s", status, body)
	}
	if strings.Contains(body, "version") || strings.Contains(body, "created_at") {
		t.Fatalf("v1 get leaked v2 fields: %s", body)
	}
}
