package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testCallbackBase = "http://192.168.10.101:30880"

// fakeShelly is a minimal stand-in for the device RPC surface. It records the
// methods called so a test can assert the reconciler took the right path.
type fakeShelly struct {
	hooks   []shellyHook
	calls   []string
	rpcErr  bool
	lastReq map[string]any
}

func (f *fakeShelly) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		f.calls = append(f.calls, req.Method)
		f.lastReq = req.Params

		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)

		if f.rpcErr {
			// Shelly reports method errors inside a 200 response.
			enc.Encode(map[string]any{ //nolint:errcheck
				"id":    1,
				"error": map[string]any{"code": -103, "message": "boom"},
			})
			return
		}

		switch req.Method {
		case "Webhook.List":
			enc.Encode(map[string]any{ //nolint:errcheck
				"id": 1, "result": map[string]any{"hooks": f.hooks, "rev": 1},
			})
		case "Webhook.Create", "Webhook.Update":
			enc.Encode(map[string]any{ //nolint:errcheck
				"id": 1, "result": map[string]any{"id": 1, "rev": 2},
			})
		default:
			http.Error(w, "unknown method", http.StatusNotFound)
		}
	})
}

func newTestClient(t *testing.T, f *fakeShelly) *shellyClient {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return newShellyClient(strings.TrimPrefix(srv.URL, "http://"), testCallbackBase)
}

// healthyHook is the shape the reconciler should leave the device in.
func healthyHook() shellyHook {
	return shellyHook{
		ID:           1,
		Enable:       true,
		Event:        shellyEvent,
		URLs:         []string{testCallbackBase + shellyCallbackPath},
		Condition:    nil,
		RepeatPeriod: 0,
	}
}

func TestCallbackURLUsesEventPlaceholder(t *testing.T) {
	c := newShellyClient("192.168.10.188", testCallbackBase)

	want := testCallbackBase + "/webhook?apower=${ev.apower}"
	if c.callbackURL != want {
		t.Fatalf("callbackURL = %q, want %q", c.callbackURL, want)
	}
	// The bare ${apower} form is silently not substituted by the device.
	if strings.Contains(c.callbackURL, "?apower=${apower}") {
		t.Fatal("callback URL uses the non-substituted ${apower} form")
	}
}

func TestNewShellyClientNormalisesInput(t *testing.T) {
	c := newShellyClient(" http://192.168.10.188/ ", testCallbackBase+"/")
	if c.addr != "192.168.10.188" {
		t.Fatalf("addr = %q, want 192.168.10.188", c.addr)
	}
	if strings.Contains(c.callbackURL, "//webhook") {
		t.Fatalf("double slash in callback URL: %q", c.callbackURL)
	}
}

func TestReconcileCreatesWhenNoHooks(t *testing.T) {
	f := &fakeShelly{}
	c := newTestClient(t, f)

	result, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result != "created" {
		t.Fatalf("result = %q, want created", result)
	}
	if got := strings.Join(f.calls, ","); got != "Webhook.List,Webhook.Create" {
		t.Fatalf("calls = %q", got)
	}
	// Throttling the event costs run-duration accuracy, so neither may be set.
	if f.lastReq["condition"] != nil {
		t.Fatalf("condition = %v, want nil", f.lastReq["condition"])
	}
	if f.lastReq["repeat_period"].(float64) != 0 {
		t.Fatalf("repeat_period = %v, want 0", f.lastReq["repeat_period"])
	}
}

func TestReconcileIsNoopWhenAlreadyCorrect(t *testing.T) {
	f := &fakeShelly{hooks: []shellyHook{healthyHook()}}
	c := newTestClient(t, f)

	result, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q, want ok", result)
	}
	if got := strings.Join(f.calls, ","); got != "Webhook.List" {
		t.Fatalf("calls = %q, want only Webhook.List", got)
	}
}

// The failure that stopped the feed: the hook existed and looked healthy, but
// its URL used the placeholder the device does not substitute.
func TestReconcileRepairsBadPlaceholder(t *testing.T) {
	h := healthyHook()
	h.ID = 7
	h.URLs = []string{testCallbackBase + "/webhook?apower=${apower}"}
	f := &fakeShelly{hooks: []shellyHook{h}}
	c := newTestClient(t, f)

	result, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result != "updated" {
		t.Fatalf("result = %q, want updated", result)
	}
	if f.lastReq["id"].(float64) != 7 {
		t.Fatalf("updated id = %v, want existing hook 7", f.lastReq["id"])
	}
	if urls := f.lastReq["urls"].([]any); urls[0].(string) != c.callbackURL {
		t.Fatalf("url = %v, want %q", urls[0], c.callbackURL)
	}
}

// A throttled hook still delivers, so nothing looks broken — but readings
// arrive too sparsely to time a run, which inflates reported runtime severalfold.
func TestReconcileRepairsThrottledHook(t *testing.T) {
	cond := "ev.apower >= 0"
	h := healthyHook()
	h.Condition = &cond
	h.RepeatPeriod = 60
	f := &fakeShelly{hooks: []shellyHook{h}}
	c := newTestClient(t, f)

	result, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result != "updated" {
		t.Fatalf("result = %q, want updated", result)
	}
	if f.lastReq["condition"] != nil {
		t.Fatalf("condition = %v, want cleared", f.lastReq["condition"])
	}
	if f.lastReq["repeat_period"].(float64) != 0 {
		t.Fatalf("repeat_period = %v, want cleared to 0", f.lastReq["repeat_period"])
	}
}

func TestReconcileRepairsDisabledHook(t *testing.T) {
	h := healthyHook()
	h.Enable = false
	f := &fakeShelly{hooks: []shellyHook{h}}
	c := newTestClient(t, f)

	result, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result != "updated" {
		t.Fatalf("result = %q, want updated", result)
	}
	if f.lastReq["enable"] != true {
		t.Fatalf("enable = %v, want true", f.lastReq["enable"])
	}
}

func TestReconcileIgnoresUnrelatedHooks(t *testing.T) {
	f := &fakeShelly{hooks: []shellyHook{{
		ID: 3, Enable: true, Event: "pm1.voltage_change",
		URLs: []string{"http://example.invalid/other"},
	}}}
	c := newTestClient(t, f)

	result, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// Someone else's hook must not be hijacked into ours.
	if result != "created" {
		t.Fatalf("result = %q, want created", result)
	}
}

func TestReconcileSurfacesShellyError(t *testing.T) {
	f := &fakeShelly{rpcErr: true}
	c := newTestClient(t, f)

	if _, err := c.reconcile(context.Background()); err == nil {
		t.Fatal("expected an error when the device reports one in a 200 body")
	}
}


