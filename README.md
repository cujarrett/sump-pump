# sump-pump-bridge

Receives Shelly webhook events and publishes them to NATS JetStream.

The meter calls `/webhook` with current wattage. Every reading becomes a
Prometheus gauge; NATS gets a message only when the pump crosses the run
threshold.

## Device config

The bridge reconciles the meter's webhook on startup and every minute, so a
device wiped by a power cut repairs itself.

| Env var | Example | Meaning |
|---|---|---|
| `SHELLY_ADDR` | `192.168.10.188` | Meter address — reconciler stays off unless set |
| `SHELLY_CALLBACK_BASE` | `http://192.168.10.101:30880` | Where the meter sends readings — reconciler stays off unless set |
| `SHELLY_RECONCILE_INTERVAL` | `1m` | Optional |
| `WATTS_THRESHOLD` | `300` | Optional, watts at or above which the pump counts as running |

The code owns the callback URL's path and query. Two device settings look
harmless and quietly kill the feed, so the reconciler enforces both.

**The placeholder.** The webhook URL ends in `?apower=${ev.apower}`. The meter
swaps `${ev.apower}` for the real wattage before calling. It does not recognise
the shorter `${apower}` — it sends those characters literally, so the bridge
receives `apower=${apower}`, cannot read a number out of it, and rejects the
call with a 400. The meter keeps calling and nothing gets recorded.

**The throttle.** With `condition` and `repeat_period` both empty, the meter
reports every time the wattage changes — it calls the instant the pump kicks on
and again the instant it stops, about two seconds apart. Set either field and it
switches to a timer instead, roughly one call every three minutes. A run lasts
about twelve seconds. At one sample per three minutes a twelve second event is
invisible: the running flag stays stuck on until the next call, so a 12s run
gets logged as 7 minutes — five times the real runtime.

Liveness therefore comes from the reconcile call itself: `shelly_webhook_configured`
is 1 only when the last reconcile reached the meter and found the hook correct.
Never add a device side heartbeat to get it — a heartbeat needs `repeat_period`,
which is the trade above.

## Manual recovery

Only if the reconciler is off or cannot reach the meter.

```bash
curl -s http://192.168.10.188/rpc/Webhook.List

curl -s -X POST http://192.168.10.188/rpc -H 'Content-Type: application/json' \
  -d '{"id":1,"method":"Webhook.Create","params":{"cid":0,"enable":true,
       "event":"pm1.apower_change",
       "urls":["http://192.168.10.101:30880/webhook?apower=${ev.apower}"]}}'
```

A rising `status_code="400"` means the meter is calling in but the reading is
unparseable, usually the wrong placeholder:

```bash
kubectl exec -n sump-pump deploy/sump-pump-bridge -c api -- \
  wget -qO- localhost:9090/metrics | grep 'path="/webhook"'
```

## Development

```bash
just ci   # lint, test, build
```

## Deployment

CI builds an ARM64 image, pushes it to GHCR, then commits the new tag to the `sump-pump` workspace in [homelab-workspaces](https://github.com/cujarrett/homelab-workspaces). ArgoCD deploys from there.

### Rotating `HOMELAB_PAT`

The `deploy` job authenticates to `cujarrett/homelab-workspaces` with `HOMELAB_PAT`, a repo-level Actions secret holding a fine-grained PAT. `cujarrett` is a personal account, not an org, so secrets cannot be shared — other repos define a secret by the same name holding their own token, and rotating one does not affect the others.

When the token expires, `deploy` fails on `Bad credentials (HTTP 401)` while `test` and `build-and-push` stay green. Images keep building and the cluster keeps running the old tag, so nothing looks broken until someone checks what is actually deployed.

```bash
# 1. Mint a replacement at https://github.com/settings/personal-access-tokens
#    Repository access — cujarrett/homelab-workspaces only
#    Permissions — Contents: Read and write

# 2. Replace the secret
print -n "Paste new token: "
read -rs NEW_TOKEN
echo
gh secret set HOMELAB_PAT --repo cujarrett/sump-pump-bridge --body "$NEW_TOKEN"
unset NEW_TOKEN

# 3. Revoke the old token, then confirm the next merge to main still deploys
gh run watch --repo cujarrett/sump-pump-bridge \
  "$(gh run list --repo cujarrett/sump-pump-bridge --branch main --limit 1 --json databaseId --jq '.[0].databaseId')"
```
