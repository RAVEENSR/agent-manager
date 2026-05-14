# Local Dev Guide

A beginner-oriented walkthrough of how the local Agent Manager environment is laid out and how to start, stop, and poke at it. For deeper docs see `documentation/docs/getting-started/`.

---

## TL;DR

```bash
# First time
make setup

# Daily — start
colima start --profile dev      # if your Mac was rebooted
make dev-up                     # bring AMP services back if stopped
make port-forward               # leave terminal open

# Daily — stop
# Ctrl+C the port-forward ter                 # stop AMP compose services (optional)
colima stop --profile dev       # stop the whole VM (frees ~8 GB RAM)
```

Open the UI at http://localhost:3000 once `make dev-up` and `make port-forward` are running.

---

## The Two Layers

The local stack runs in two independent layers:

```
┌────────────────────────────────────────────────────────────┐
│ Layer 1 — Docker Compose (on host Docker)                 │
│   agent-manager-console   :3000                           │
│   agent-manager-service   :9000  (gRPC :9243)             │
│   agent-manager-db        :5432  (Postgres)               │
└────────────────────────────────────────────────────────────┘
┌────────────────────────────────────────────────────────────┐
│ Layer 2 — k3d (Kubernetes inside the Colima VM)           │
│   ~50 pods across these namespaces:                        │
│     amp-thunder                       Identity provider    │
│     openchoreo-control-plane          Control plane API    │
│     openchoreo-data-plane             Gateways             │
│     openchoreo-workflow-plane         Argo, registry       │
│     openchoreo-observability-plane    OpenSearch, traces…  │
│     openbao                           Secrets store        │
│     cert-manager / external-secrets   Infra                │
└────────────────────────────────────────────────────────────┘
```

Both layers run on the same Colima VM (`docker context` = `colima-dev`).

- **Layer 1** is what you typically *touch* during development — the AMP console and API. Iterating on AMP code only requires bouncing this layer.
- **Layer 2** is the platform that AMP depends on: it gives you Thunder for auth, OpenBao for secrets, OpenChoreo for orchestration, observability for traces. Once it's up, you rarely need to touch it.

---

## Component Map

| Component | Layer | Where | Local URL (with `make port-forward`) |
|---|---|---|---|
| Console (UI) | Compose | `agent-manager-console` | http://localhost:3000 |
| AMP Service API | Compose | `agent-manager-service` | http://localhost:9000 |
| Postgres | Compose | `agent-manager-db` | postgresql://agentmanager:agentmanager@localhost:5432/agentmanager |
| Thunder (IDP) | k3d | `amp-thunder` / `amp-thunder-extension-service` | http://localhost:8090 |
| OpenChoreo API | k3d | `openchoreo-control-plane` / `openchoreo-api` | http://localhost:8195 |
| OpenBao (secrets) | k3d | `openbao` / `openbao` (token: `root`) | http://localhost:8200 |
| Observer API | k3d | `openchoreo-observability-plane` / `observer` | http://localhost:8085 |
| Traces Observer | k3d | `openchoreo-observability-plane` / `amp-traces-observer` | http://localhost:9098 |
| OpenSearch | k3d | `openchoreo-observability-plane` / `opensearch` | http://localhost:9200 |
| AI Gateway Runtime | k3d | `openchoreo-data-plane` / `default-ai-gateway-gateway-runtime` | http://localhost:8084 |
| OTel Collector | k3d | `openchoreo-observability-plane` / `opentelemetry-collector` | http://localhost:21893 |
| Obs Gateway | k3d | `openchoreo-data-plane` / `obs-gateway-gateway-gateway-runtime` | http://localhost:22893/otel |

---

## Day-1 Setup

```bash
make setup
```

Runs end-to-end:
1. Starts Colima `dev` profile
2. Creates k3d cluster `openchoreo-local-setup`
3. Installs prerequisites (Gateway API CRDs, cert-manager, External Secrets, Kgateway, OpenBao)
4. Installs OpenChoreo (control / data / workflow / observability planes)
5. Installs AMP extensions (Thunder, evaluation workflows)
6. Builds & loads local Docker images (`amp-traces-observer`, `amp-evaluation-monitor`)
7. Generates JWT keys
8. Starts the Docker Compose AMP services
9. Builds the console locally with Rush

Takes ~10–15 min on first run.

### Required Node version

The setup uses both `agent-manager-service` tooling and Rush (for the console). The intersecting range that works for both is **Node 20.19.x to 20.x**. If you use nvm:

```bash
nvm install 20
nvm alias default 20
```

---

## Daily Start / Stop

| Goal | Command | Stops compose | Stops k3d | Frees VM RAM |
|---|---|---|---|---|
| Stop AMP services only | `make dev-down` | yes | no | no |
| Stop the cluster (and compose) | `colima stop --profile dev` | yes (VM goes away) | yes | yes (~8 GB) |
| Wipe everything | `make teardown` | yes | yes (deletes cluster) | yes (deletes VM) |
| Start compose services | `make dev-up` | — | — | — |
| Start the VM | `colima start --profile dev` | — | — | — |
| Restart compose | `make dev-restart` | — | — | — |
| Rebuild compose images | `make dev-rebuild` | — | — | — |

What persists:
- `make dev-down` and `colima stop` preserve all data (volumes, secrets, OpenBao kv).
- `make teardown` deletes the cluster and the Colima `dev` VM — full wipe.

---

## Accessing the Cluster from a New Terminal

```bash
# 1. Make sure the VM is up
colima status --profile dev || colima start --profile dev

# 2. Use the right Docker context
docker context use colima-dev

# 3. Point kubectl at k3d
kubectl config use-context k3d-openchoreo-local-setup

# 4. Sanity check
kubectl get nodes
kubectl get ns
```

If the kubectl context is missing:

```bash
k3d kubeconfig merge openchoreo-local-setup \
  --kubeconfig-merge-default --kubeconfig-switch-context
```

### Browse pods, logs, exec

```bash
kubectl get pods -A
kubectl get pods,svc -n amp-thunder

kubectl logs -n amp-thunder deploy/amp-thunder-extension-deployment -f
kubectl exec -it -n openchoreo-observability-plane opensearch-master-0 -- /bin/sh
kubectl describe pod <pod> -n <ns>
```

Tip: `brew install k9s` then run `k9s` for an interactive TUI. Press `:` and type `pods` or a namespace name.

### Port-forward

```bash
cd ~/WSO2/Public/agent-manager
make port-forward          # leave terminal open
```

Forwards 9 cluster services to localhost (see Component Map). Stop with `Ctrl+C`.

One-off forward for a single service:

```bash
kubectl port-forward -n amp-thunder svc/amp-thunder-extension-service 8090:8090
```

### Why port-forward is needed

k3d runs Kubernetes inside Docker inside the Colima VM. Cluster services have `ClusterIP`s that don't exist on your laptop's network. `kubectl port-forward` opens a tunnel from `localhost:<port>` on your Mac through the API server to the pod. Tunnels die when the process exits, so the terminal must stay open while you need the URLs.

Compose services (`:3000`, `:9000`, `:5432`) don't need port-forward — Docker publishes those ports on the host directly.

---

## Compose-side Commands

```bash
docker compose -f deployments/docker-compose.yml ps
docker compose -f deployments/docker-compose.yml logs -f agent-manager-service

# Convenience targets
make dev-logs          # follow all compose logs
make service-logs      # AMP service logs
make service-shell     # shell into the AMP service container
make console-logs      # console logs
make db-connect        # psql into the AMP DB
make db-logs           # postgres logs
make dev-migrate       # run DB migrations
```

---

## Common Gotchas

### 1. OpenBao readiness wait times out

The original `setup-prerequisites.sh` waited 120s for OpenBao to be ready. On a fresh cluster, image pull + the postStart hook (kv writes) can take longer. Already bumped to **300s**. If it still times out, increase `setup-prerequisites.sh:97` or re-run setup — the helm release will be skipped on the second pass.

### 2. Node version range

- `setup-platform` requires `>= 20.19.0` or `>= 22.12.0`.
- Rush (`setup-console-local`) requires `>= 18.20.3 < 19 || >= 20.14.0 < 21`.
- The intersection is **Node 20.19.x to 20.x** (e.g. 20.20.2). Node 23 will fail Rush. Node 20.14 will fail platform setup.

### 3. `DOCKER_DEFAULT_PLATFORM=linux/amd64` in your shell

Forces every `docker build` to amd64, even on Apple Silicon. Result: locally-built images (`amp-traces-observer`, `amp-evaluation-monitor`) are amd64 while the cluster is arm64, and pods land in `ImagePullBackOff` with "no match for platform in manifest". Fix:

```bash
unset DOCKER_DEFAULT_PLATFORM
# or in ~/.zshrc, comment the export line
```

Rebuild the affected images:

```bash
cd traces-observer-service && make docker-load-k3d
cd ../evaluation-job        && make docker-load-k3d
kubectl --context k3d-openchoreo-local-setup rollout restart \
  deploy/amp-traces-observer -n openchoreo-observability-plane
```

### 4. Port-forward already in use

If a second `make port-forward` errors with "address already in use":

```bash
pkill -f 'kubectl port-forward'
make port-forward
```

### 5. Cluster context missing after Mac reboot

```bash
colima start --profile dev
docker context use colima-dev
# context usually still works; if not:
k3d kubeconfig merge openchoreo-local-setup \
  --kubeconfig-merge-default --kubeconfig-switch-context
```

### 6. k3s crashloops after `colima stop` → `colima start`

Stopping Colima while k3d is running leaves the k3s server unable to rebind to the node IP after restart. Symptoms: `kubectl` returns `Unable to connect to the server: EOF`, the k3s container's restart count climbs, logs show `Failed to start networking: ... failed to find interface with specified node ip`, and pods stay in `Pending` for tens of minutes.

Recover:

```bash
k3d cluster stop  openchoreo-local-setup
k3d cluster start openchoreo-local-setup --wait
```

Avoid by always stopping k3d **before** Colima:

```bash
k3d cluster stop openchoreo-local-setup
colima stop --profile dev
```

If pods stay `Pending` after restart with the node tainted `node.kubernetes.io/unschedulable:NoSchedule`, remove the stale taint:

```bash
kubectl taint nodes k3d-openchoreo-local-setup-server-0 \
  node.kubernetes.io/unschedulable:NoSchedule-
```

### 7. Buildpack deploys crash with Rosetta sigreturn error on macOS 26 (Apple Silicon)

`setup-colima.sh` starts Colima with `--vz-rosetta` to allow amd64 emulation. On macOS 26.x, the Apple buildpack lifecycle binary crashes inside Rosetta with `rosetta error: unexpectedly got a signal during sigreturn` and the deploy workflow fails at the `build-image` step with `failed with status code: 133`.

Disable Rosetta and fall back to QEMU for amd64 emulation:

```bash
colima stop --profile dev
colima start --profile dev --vm-type=vz --vz-rosetta=false --network-address --cpu 4 --memory 8
```

`rosetta: false` is then persisted in `~/.colima/dev/colima.yaml` and survives future `colima start` calls. Trade-off: amd64 builds run noticeably slower (2–4×), but they no longer crash. Re-running `make setup-colima` will re-enable Rosetta — re-apply this fix afterward.

After the restart, also start the k3d nodes if they don't auto-start:

```bash
docker start openchoreo-local-control-plane openchoreo-local-worker
```

### 8. `make setup` does not run database migrations

`make setup` brings up the Postgres container but does not apply the schema. The console will return `INTERNAL_ERROR: Failed to list configurations` and the service logs will show:

```
ERROR: relation "agent_configurations" does not exist (SQLSTATE 42P01)
ERROR: relation "monitors" does not exist (SQLSTATE 42P01)
```

Run migrations manually after setup:

```bash
make dev-migrate
```

If your local Go is older than 1.25 (`make dev-migrate` will fail with `go.mod requires go >= 1.25.0`), either set `GOTOOLCHAIN=auto` so Go auto-downloads the toolchain, or run the migration inside the already-built service container which has the right Go baked in:

```bash
docker exec agent-manager-service /app/tmp/agent-manager-service -migrate -server=false
```

### 9. Deploy fails at `generate-workload-cr` with `apk add jq` DNS errors

The `amp-generate-workload` ClusterWorkflowTemplate runs `apk add --no-cache jq` inside an alpine-based podman runner. Alpine's musl resolver has known issues with parallel A+AAAA queries against in-cluster CoreDNS, so `dl-cdn.alpinelinux.org` lookups fail intermittently:

```
WARNING: fetching https://dl-cdn.alpinelinux.org/alpine/v3.20/main: DNS lookup error
ERROR: unable to select packages:
  jq (no such package)
```

Same-pod requests against `ghcr.io`/`docker.io` succeed because skopeo uses Go's resolver, not musl's — confirms it's a musl bug, not a CoreDNS outage.

Workaround — patch the template with `podSpecPatch` to set `ndots:1`:

```bash
kubectl patch clusterworkflowtemplate amp-generate-workload --type=json -p='[
  {"op":"add","path":"/spec/templates/0/podSpecPatch","value":"dnsConfig:\n  options:\n  - name: ndots\n    value: \"1\"\n"}
]'
```

Helm-managed — re-applied on every `make setup-platform`. The durable fix is to bypass `apk add jq` in the template script and instead extract the jq binary from `ghcr.io/jqlang/jq:1.7.1` (which the same step already pulls successfully).

### 10. Trace ingest gets 401, no traces appear in the console

Symptom: agent runs (`POST /chat 200 OK` in the pod logs) but the traces tab and `model-configs/scores` endpoints stay empty. Backend returns `BAD_REQUEST: must be between 1 and 100` because the console sends `limit=0` when no traces exist (a frontend bug in `Traces.Component.tsx:203`).

The real cause is that the obs-gateway is rejecting trace exports:

```
ERROR:opentelemetry.exporter.otlp.proto.http.trace_exporter:
Failed to export span batch code: 401, reason: Unauthorized
```

The gateway's JWT validator is configured to fetch the JWKS from `host.docker.internal:9000`, which **does not resolve inside k3d clusters running on Colima** (only Docker Desktop wires that alias). With JWKS unreachable, every JWT is rejected.

**Already fixed in source** — `setup-openchoreo.sh` now rewrites both the JWKS URI and the controller `controlPlane.host` to `host.k3d.internal` when generating `api-platform-operator-local-config.yaml`. New `make setup` runs work end-to-end.

For an existing setup (where the ConfigMap was generated before this fix), patch live:

```bash
kubectl get cm obs-gateway-config -n openchoreo-data-plane -o json \
  | python3 -c "import json,sys; d=json.load(sys.stdin); d['data']['values.yaml']=d['data']['values.yaml'].replace('host.docker.internal','host.k3d.internal'); print(json.dumps(d))" \
  | kubectl apply -f -

kubectl rollout restart deploy/obs-gateway-gateway-controller       -n openchoreo-data-plane
kubectl rollout restart deploy/obs-gateway-gateway-gateway-runtime  -n openchoreo-data-plane
```

Verify reachability before invoking the agent:

```bash
kubectl exec -n openchoreo-data-plane deploy/obs-gateway-gateway-gateway-runtime -- \
  curl -s -o /dev/null -w "%{http_code}\n" \
  http://host.k3d.internal:9000/auth/external/jwks.json
# expect: 200
```

Then send a chat message from the console and confirm the `Failed to export span batch code: 401` errors stop in the agent's pod logs.

### 11. `make port-forward` says "k3d cluster is not running" but the containers are up

Symptom: `docker ps` shows both `k3d-openchoreo-local-setup-server-0` and `…-serverlb` as `Up`, `k3d cluster list` reports `1/1` servers, but `make port-forward` (and `kubectl cluster-info --context k3d-openchoreo-local-setup`) fails with:

```
Unable to connect to the server: net/http: TLS handshake timeout
```

TCP connects to `127.0.0.1:6550` (and 8080, 8443, 19080, …) succeed, but the TLS handshake hangs — every Colima-forwarded port behaves the same way. From inside the cluster everything is healthy (`curl https://k3d-openchoreo-local-setup-server-0:6443/healthz` from the LB container returns a normal `401 Unauthorized`).

Cause: Lima's host-agent port forwarder (`limactl hostagent`) — the process Colima uses to forward macOS host ports into the VM — has gone stale after long VM uptime. It still accepts TCP, but no longer pipes data through. `k3d cluster stop/start` will not fix it, because k3d only restarts the containers inside the VM, not the host-side forwarder.

Fix — restart Colima itself:

```bash
colima restart -p dev
```

This reboots the VM (~30 s), restarts the Lima host-agent with fresh port forwards, and brings the k3d containers back automatically. Cluster state on the Colima disk is preserved. After it's up, `kubectl cluster-info` returns immediately and `make port-forward` passes the cluster check.

---

## When Something Is Broken

```bash
# Find the unhappy pods
kubectl get pods -A | grep -vE 'Running|Completed'

# Describe and read events
kubectl describe pod <pod> -n <ns>

# Logs (current and previous run)
kubectl logs -n <ns> <pod>
kubectl logs -n <ns> <pod> --previous

# Image arch — when ImagePullBackOff says "no match for platform"
docker exec k3d-openchoreo-local-setup-server-0 \
  ctr -n k8s.io images list | grep <image>

# Port-forward died — just restart it
make port-forward
```

If the cluster is genuinely wedged, `make teardown` then `make setup` is the nuclear option — takes ~10 min but always works.

---

## Quick Reference

```bash
# Cluster context
kubectl config use-context k3d-openchoreo-local-setup

# All pods
kubectl get pods -A

# Port-forward all
make port-forward

# Compose status
docker compose -f deployments/docker-compose.yml ps

# OpenBao peek (after port-forward)
export BAO_ADDR=http://127.0.0.1:8200 BAO_TOKEN=root
bao kv list secret/

# Stop everything
make dev-down && colima stop --profile dev

# Wipe everything
make teardown
```
