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

		result := map[string]any{"id": 1}
		if req.Method == "Webhook.List" {
			result = map[string]any{"hooks": f.hooks}
		}
		enc.Encode(map[string]any{"id": 1, "result": result}) //nolint:errcheck
	})
}

func newTestClient(t *testing.T, f *fakeShelly) *shellyClient {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return newShellyClient(srv.URL, testCallbackBase)
}

// healthyHook is the shape the reconciler should leave the device in.
func healthyHook() shellyHook {
	return shellyHook{
		ID:     1,
		Enable: true,
		Event:  shellyEvent,
		URLs:   []string{testCallbackBase + shellyCallbackPath},
	}
}

func TestCallbackURLUsesEventPlaceholder(t *testing.T) {
	c := newShellyClient("192.168.10.188", testCallbackBase)

	// The bare ${apower} form is silently not substituted by the device.
	want := testCallbackBase + "/webhook?apower=${ev.apower}"
	if c.callbackURL != want {
		t.Fatalf("callbackURL = %q, want %q", c.callbackURL, want)
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

// Every way the device drifts from desired state, and what the repair must fix.
// A throttled hook is the subtle one: it still delivers, so nothing looks
// broken, but too sparsely to time a run.
func TestReconcileRepairsDrift(t *testing.T) {
	throttled := "ev.apower >= 0"

	cases := []struct {
		name   string
		mutate func(*shellyHook)
		check  func(*testing.T, map[string]any)
	}{
		{
			name:   "placeholder the device does not substitute",
			mutate: func(h *shellyHook) { h.URLs = []string{testCallbackBase + "/webhook?apower=${apower}"} },
			check: func(t *testing.T, req map[string]any) {
				if urls := req["urls"].([]any); urls[0].(string) != testCallbackBase+shellyCallbackPath {
					t.Fatalf("url = %v", urls[0])
				}
			},
		},
		{
			name:   "condition set",
			mutate: func(h *shellyHook) { h.Condition = &throttled },
			check: func(t *testing.T, req map[string]any) {
				if req["condition"] != nil {
					t.Fatalf("condition = %v, want cleared", req["condition"])
				}
			},
		},
		{
			name:   "repeat_period set",
			mutate: func(h *shellyHook) { h.RepeatPeriod = 60 },
			check: func(t *testing.T, req map[string]any) {
				if req["repeat_period"].(float64) != 0 {
					t.Fatalf("repeat_period = %v, want cleared", req["repeat_period"])
				}
			},
		},
		{
			name:   "disabled",
			mutate: func(h *shellyHook) { h.Enable = false },
			check: func(t *testing.T, req map[string]any) {
				if req["enable"] != true {
					t.Fatalf("enable = %v, want true", req["enable"])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := healthyHook()
			h.ID = 7
			tc.mutate(&h)
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
				t.Fatalf("updated id = %v, want the existing hook", f.lastReq["id"])
			}
			tc.check(t, f.lastReq)
		})
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
