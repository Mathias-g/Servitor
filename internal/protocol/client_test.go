package protocol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordRequest captures what a daemon handler receives.
type recordRequest struct {
	method string
	path   string
	body   string
}

// newTestServer returns an httptest server whose handler records the request
// and answers with the given status and body.
func newTestServer(t *testing.T, status int, body string) (*httptest.Server, *recordRequest) {
	t.Helper()
	rec := &recordRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		rec.body = string(b)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func clientFor(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewClient(strings.TrimPrefix(srv.URL, "http://"))
}

func TestHealthAndStopUseGETPOSTOnTheirPaths(t *testing.T) {
	ctx := context.Background()

	srv, rec := newTestServer(t, http.StatusOK, "")
	c := clientFor(t, srv)
	if err := c.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != PathHealth {
		t.Fatalf("Health hit %s %s, want GET %s", rec.method, rec.path, PathHealth)
	}

	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != PathStop {
		t.Fatalf("Stop hit %s %s, want POST %s", rec.method, rec.path, PathStop)
	}
}

func TestSubmitUpdateSendBodyOnTheirPaths(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		call func(*Client) error
		path string
	}{
		{func(c *Client) error { _, err := c.Submit(ctx, []byte("wafer: yaml")); return err }, PathSubmit},
		{func(c *Client) error { _, err := c.Update(ctx, []byte("wafer: yaml")); return err }, PathUpdate},
	} {
		srv, rec := newTestServer(t, http.StatusOK, "")
		c := clientFor(t, srv)
		if err := tc.call(c); err != nil {
			t.Fatalf("call %s: %v", tc.path, err)
		}
		if rec.method != http.MethodPost || rec.path != tc.path {
			t.Fatalf("call %s hit %s %s, want POST %s", tc.path, rec.method, rec.path, tc.path)
		}
		if rec.body != "wafer: yaml" {
			t.Fatalf("call %s body = %q, want the wafer YAML", tc.path, rec.body)
		}
	}
}

func TestEnableDisableTriggerPassNameQuery(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		call func(*Client) error
		path string
	}{
		{func(c *Client) error { return c.Enable(ctx, "wf name") }, PathEnable},
		{func(c *Client) error { return c.Disable(ctx, "wf name") }, PathDisable},
		{func(c *Client) error { return c.Trigger(ctx, "wf name", []byte(`{"k":"v"}`)) }, PathTrigger},
	} {
		srv, rec := newTestServer(t, http.StatusOK, "")
		c := clientFor(t, srv)
		if err := tc.call(c); err != nil {
			t.Fatalf("call %s: %v", tc.path, err)
		}
		if rec.method != http.MethodPost {
			t.Fatalf("call %s method = %s, want POST", tc.path, rec.method)
		}
		if !strings.HasPrefix(rec.path, tc.path+"?name=wf+name") {
			t.Fatalf("call %s query = %q, want name=wf+name", tc.path, rec.path)
		}
		if tc.path == PathTrigger && rec.body != `{"k":"v"}` {
			t.Fatalf("trigger body = %q, want the inputs", rec.body)
		}
	}
}

func TestListRunsAndGetRunUseGETWithQuery(t *testing.T) {
	ctx := context.Background()

	srv, rec := newTestServer(t, http.StatusOK, `[{"id":"r1"}]`)
	c := clientFor(t, srv)
	body, err := c.ListRuns(ctx)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != PathRuns {
		t.Fatalf("ListRuns hit %s %s, want GET %s", rec.method, rec.path, PathRuns)
	}
	if body != `[{"id":"r1"}]` {
		t.Fatalf("ListRuns body = %q, want the raw history", body)
	}

	srv2, rec2 := newTestServer(t, http.StatusOK, `{"run":{"id":"r1"},"nodes":[]}`)
	c2 := clientFor(t, srv2)
	body2, err := c2.GetRun(ctx, "r1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if rec2.method != http.MethodGet || !strings.HasPrefix(rec2.path, PathRun+"?id=r1") {
		t.Fatalf("GetRun hit %s %s, want GET %s?id=r1", rec2.method, rec2.path, PathRun)
	}
	// The run response carries node outcomes under a `nodes` key.
	var runResp map[string]any
	if err := json.Unmarshal([]byte(body2), &runResp); err != nil {
		t.Fatalf("GetRun body not JSON: %v", err)
	}
	if _, ok := runResp["nodes"]; !ok {
		t.Fatalf("GetRun body %q missing nodes key", body2)
	}
}

func TestCancelPassesIDQuery(t *testing.T) {
	ctx := context.Background()
	srv, rec := newTestServer(t, http.StatusOK, "")
	c := clientFor(t, srv)
	if err := c.Cancel(ctx, "run-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != PathCancel+"?id=run-1" {
		t.Fatalf("Cancel hit %s %s, want POST %s?id=run-1", rec.method, rec.path, PathCancel)
	}
}

func TestNonOKStatusReturnsError(t *testing.T) {
	ctx := context.Background()
	srv, rec := newTestServer(t, http.StatusInternalServerError, "boom")
	c := clientFor(t, srv)
	if err := c.Health(ctx); err == nil {
		t.Fatal("expected an error on non-200")
	}
	_ = rec
}
