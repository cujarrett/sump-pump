package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Shelly only substitutes the ${ev.<attr>} form. A bare ${apower} is passed
// through as a literal, which webhookHandler rejects as a 400 — the device
// keeps calling and nothing is ever recorded.
const shellyCallbackPath = "/webhook?apower=${ev.apower}"

// The hook carries no condition and no repeat_period, and that is deliberate.
//
// sump_pump_running is a gauge that holds its last value until the next
// delivery, so run duration is only ever as accurate as the delivery rate.
// Unthrottled pm1.apower_change fires on both edges of a run within a second or
// two, which is what makes a twelve second run measurable. Setting either field
// throttles the event to roughly one call every three minutes and inflates
// reported runtime about fivefold.
//
// Liveness comes from the reconcile call itself, so there is no need for a
// device side heartbeat — which would cost exactly the accuracy above.
const shellyEvent = "pm1.apower_change"

type shellyHook struct {
	ID           int      `json:"id"`
	Enable       bool     `json:"enable"`
	Event        string   `json:"event"`
	URLs         []string `json:"urls"`
	Condition    *string  `json:"condition"`
	RepeatPeriod int      `json:"repeat_period"`
}

type shellyMetrics struct {
	configured prometheus.Gauge
	lastSeen   prometheus.Gauge
}

type shellyClient struct {
	addr        string
	callbackURL string
	hc          *http.Client
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func newShellyClient(addr, callbackBase string) *shellyClient {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")

	return &shellyClient{
		addr:        strings.TrimSuffix(addr, "/"),
		callbackURL: strings.TrimSuffix(strings.TrimSpace(callbackBase), "/") + shellyCallbackPath,
		hc:          &http.Client{Timeout: 10 * time.Second},
	}
}

// rpc calls one Shelly JSON-RPC method. A Shelly-level error comes back inside
// a 200, so the body has to be checked even on success.
func (c *shellyClient) rpc(ctx context.Context, method string, params, out any) error {
	body := map[string]any{"id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.addr+"/rpc", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %d", method, resp.StatusCode)
	}

	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("%s: decode: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("%s: shelly error %d: %s", method, env.Error.Code, env.Error.Message)
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (c *shellyClient) matchesDesired(h shellyHook) bool {
	// A condition or repeat_period left on the hook throttles deliveries and
	// silently wrecks duration accuracy, so treat either as drift to repair.
	return h.Enable && h.Event == shellyEvent &&
		h.Condition == nil && h.RepeatPeriod == 0 &&
		len(h.URLs) == 1 && h.URLs[0] == c.callbackURL
}

func (c *shellyClient) hookParams() map[string]any {
	return map[string]any{
		"enable":        true,
		"event":         shellyEvent,
		"urls":          []string{c.callbackURL},
		"condition":     nil,
		"repeat_period": 0,
	}
}

// reconcile makes the device's webhook match what this bridge expects. It is
// idempotent, so the startup call and the periodic call are the same code path
// — and that is also the repair path after a power loss wipes device flash.
// Returns "ok", "created" or "updated".
func (c *shellyClient) reconcile(ctx context.Context) (string, error) {
	var list struct {
		Hooks []shellyHook `json:"hooks"`
	}
	if err := c.rpc(ctx, "Webhook.List", nil, &list); err != nil {
		return "", err
	}

	// Match on the event rather than the URL. A hook aimed at a stale node IP
	// is the one to correct, not a reason to add a second hook beside it.
	for _, h := range list.Hooks {
		if h.Event != shellyEvent {
			continue
		}
		if c.matchesDesired(h) {
			return "ok", nil
		}
		params := c.hookParams()
		params["id"] = h.ID
		return "updated", c.rpc(ctx, "Webhook.Update", params, nil)
	}

	params := c.hookParams()
	params["cid"] = 0
	return "created", c.rpc(ctx, "Webhook.Create", params, nil)
}

// run reconciles once immediately, then on every tick. It never returns an
// error: an unreachable Shelly must not stop the bridge from serving, since
// after an outage the device may take minutes to rejoin WiFi.
//
// A successful reconcile is also the liveness signal — the device answered.
func (c *shellyClient) run(ctx context.Context, interval time.Duration, m *shellyMetrics) {
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		tick, cancel := context.WithTimeout(ctx, 15*time.Second)
		result, err := c.reconcile(tick)
		cancel()

		switch {
		case err != nil:
			m.configured.Set(0)
			log.Printf("shelly reconcile: %v", err)
		default:
			m.configured.Set(1)
			m.lastSeen.Set(float64(time.Now().Unix()))
			if result != "ok" {
				log.Printf("shelly webhook %s", result)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// startShellyReconciler wires the reconciler if it is configured. Both env vars
// are required: without them the bridge behaves exactly as it did before.
func startShellyReconciler(ctx context.Context, m *shellyMetrics) {
	addr := getenv("SHELLY_ADDR", "")
	base := getenv("SHELLY_CALLBACK_BASE", "")
	if addr == "" || base == "" {
		log.Printf("shelly reconciler disabled (set SHELLY_ADDR and SHELLY_CALLBACK_BASE)")
		return
	}

	interval := time.Minute
	if d, err := time.ParseDuration(getenv("SHELLY_RECONCILE_INTERVAL", "1m")); err == nil && d > 0 {
		interval = d
	}

	c := newShellyClient(addr, base)
	log.Printf("shelly reconciler on %s every %s -> %s", c.addr, interval, c.callbackURL)
	go c.run(ctx, interval, m)
}
