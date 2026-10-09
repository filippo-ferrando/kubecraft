#!/usr/bin/env bash
# End-to-end test: k3s in Docker, the Helm chart, a real Paper server, and a
# Minecraft status ping through mc-router by hostname. Needs Docker and kubectl.
# Uses its own kubeconfig, never the current kube context.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
NAME=kubecraft-e2e
API_PORT=${API_PORT:-16443}
MC_PORT=${MC_PORT:-35565}
UI_PORT=${UI_PORT:-18081}
export KUBECONFIG=$WORK/kubeconfig
JAR=$WORK/jar
BASE=http://localhost:$UI_PORT
PF_PID=""

cleanup() {
  [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null || true
  if [ -z "${KEEP:-}" ]; then
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    rm -rf "$WORK"
  else
    echo "KEEP set: cluster $NAME kept, KUBECONFIG=$KUBECONFIG"
  fi
}
trap cleanup EXIT

step() { echo "==> $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

api() {
  local method=$1 path=$2 body=${3:-}
  curl -sf -b "$JAR" -c "$JAR" -H "Origin: $BASE" -H "Content-Type: application/json" \
    -H "X-CSRF-Token: $(cat "$WORK/csrf" 2>/dev/null)" -X "$method" ${body:+-d "$body"} "$BASE/api$path"
}
json() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

step "starting k3s"
docker run -d --name "$NAME" --privileged --tmpfs /run --tmpfs /var/run \
  -p "127.0.0.1:$API_PORT:6443" -p "127.0.0.1:$MC_PORT:25565" \
  rancher/k3s:latest server --disable traefik --tls-san 127.0.0.1 >/dev/null
for _ in $(seq 60); do docker exec "$NAME" kubectl get nodes 2>/dev/null | grep -q " Ready" && break; sleep 2; done
docker exec "$NAME" cat /etc/rancher/k3s/k3s.yaml | sed "s#127.0.0.1:6443#127.0.0.1:$API_PORT#" >"$KUBECONFIG"

step "building and importing the image"
docker build -q -t kubecraft:e2e "$ROOT" >/dev/null
docker save kubecraft:e2e | docker exec -i "$NAME" ctr images import - >/dev/null

step "installing postgres and the chart"
kubectl create ns minecraft >/dev/null
kubectl -n minecraft apply -f - >/dev/null <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: pg}
spec:
  selector: {matchLabels: {app: pg}}
  template:
    metadata: {labels: {app: pg}}
    spec:
      containers:
        - name: pg
          image: postgres:17-alpine
          env:
            - {name: POSTGRES_PASSWORD, value: "pw/with+special=chars"}
            - {name: POSTGRES_USER, value: kubecraft}
            - {name: POSTGRES_DB, value: kubecraft}
---
apiVersion: v1
kind: Service
metadata: {name: pg}
spec:
  selector: {app: pg}
  ports: [{port: 5432}]
---
apiVersion: v1
kind: Secret
metadata: {name: kubecraft-db}
stringData: {username: kubecraft, password: "pw/with+special=chars"}
EOF
docker run --rm --network host -v "$ROOT/charts:/apps" -v "$KUBECONFIG:/kubeconfig:ro" -e KUBECONFIG=/kubeconfig -w /apps \
  alpine/helm:latest install kc kubecraft -n minecraft --wait --timeout 5m \
  --set image.repository=kubecraft --set image.tag=e2e --set image.pullPolicy=Never \
  --set manager.uiHost=localhost:$UI_PORT --set manager.cookieSecure=false --set manager.serverBaseDomain=mine.test \
  --set secrets.sessionSecret=e2e-session-secret-e2e-session-secret \
  --set secrets.bootstrapAdminUser=admin --set secrets.bootstrapAdminPassword=admin-password \
  --set mcRouter.autoScale.downAfter=1m \
  --set database.host=pg.minecraft.svc --set database.sslmode=disable --set database.credentialsSecret.name=kubecraft-db >/dev/null

kubectl -n minecraft port-forward svc/kc-kubecraft "$UI_PORT:80" >/dev/null 2>&1 &
PF_PID=$!
for _ in $(seq 30); do curl -sf "$BASE/healthz" >/dev/null && break; sleep 1; done

step "logging in and changing the bootstrap password"
api POST /auth/login '{"username":"admin","password":"admin-password"}' | json 'd["csrf_token"]' >"$WORK/csrf"
api POST /me/password '{"current_password":"admin-password","new_password":"admin-password-2"}' >/dev/null

step "searching the plugin catalog"
api GET "/catalog/search?type=PAPER&version=LATEST&q=chunky" | json '[h["slug"] for h in d["result"]["hits"]]' | grep -q "'chunky'" || fail "chunky not found in catalog"

step "creating a Paper server with a plugin"
PLAN=$(api GET /plans | json '[p["id"] for p in d if p["name"]=="Small"][0]')
ID=$(api POST /servers "{\"slug\":\"e2e\",\"type\":\"PAPER\",\"version\":\"LATEST\",\"plan_id\":\"$PLAN\",\"settings\":{\"motd\":\"kubecraft e2e\",\"modrinth_projects\":[\"chunky\"]},\"accept_eula\":true}" | json 'd["id"]')

state() { api GET "/servers/$ID" | json 'd["status"]["state"]'; }
wait_state() {
  for _ in $(seq 90); do
    s=$(state)
    [ "$s" = "$1" ] && return 0
    case "$s" in error | crashloop) fail "server in state $s" ;; esac
    sleep 5
  done
  fail "timeout waiting for $1"
}
wait_state running

step "pinging through mc-router"
out=$(docker run --rm --network host --add-host "e2e.mine.test:127.0.0.1" itzg/mc-monitor status --host e2e.mine.test --port "$MC_PORT")
echo "$out"
echo "$out" | grep -q "motd='kubecraft e2e'" || fail "unexpected ping result"

step "checking pod limits and heap"
kubectl -n minecraft get pod mc-e2e-0 -o jsonpath='{.spec.containers[0].resources.limits.memory}' | grep -qx 2Gi || fail "memory limit"
kubectl -n minecraft get pod mc-e2e-0 -o jsonpath='{.spec.containers[0].env[?(@.name=="MEMORY")].value}' | grep -qx 1536M || fail "heap"

step "checking the plugin was downloaded"
kubectl -n minecraft exec mc-e2e-0 -c minecraft -- ls /data/plugins | grep -i chunky || fail "plugin not installed"

step "scraping prometheus metrics"
kubectl -n minecraft port-forward svc/kc-kubecraft-metrics 19090:9090 >/dev/null 2>&1 &
MPF=$!
sleep 15 # let the poller collect players and usage
m=$(curl -sf localhost:19090/metrics) || fail "metrics endpoint"
kill $MPF 2>/dev/null || true
for want in 'kubecraft_server_state{server="e2e",state="running"} 1' 'kubecraft_server_players_online{server="e2e"} 0' \
  'kubecraft_logins_total{result="success"}' 'kubecraft_reconcile_runs_total{result="ok"}' 'kubecraft_server_memory_usage_bytes{server="e2e"}'; do
  grep -qF "$want" <<<"$m" || fail "metrics missing: $want"
done
curl -sf "$BASE/metrics" | grep -q kubecraft_ && fail "metrics leak on the UI port"

step "editing a plugin config"
api GET "/servers/$ID/files?path=plugins/Chunky" | json '[e["name"] for e in d["entries"]]' | grep -q "config.yml" || fail "plugin config not listed"
CONF=$(api GET "/servers/$ID/files/content?path=plugins/Chunky/config.yml" | json 'd["content"]')
NEW="$CONF
# edited by kubecraft e2e"
api PUT "/servers/$ID/files/content" "$(python3 -c 'import json,sys;print(json.dumps({"path":"plugins/Chunky/config.yml","content":sys.argv[1]}))' "$NEW")" >/dev/null
kubectl -n minecraft exec mc-e2e-0 -c minecraft -- grep -q "edited by kubecraft e2e" /data/plugins/Chunky/config.yml || fail "config not written"

backup_has_world() {
  curl -sf -b "$JAR" "$BASE/api/servers/$ID/backup?scope=world" -o "$WORK/world.tar.gz" || fail "backup download"
  tar -tzf "$WORK/world.tar.gz" | grep -q "^world/level.dat$" || fail "backup without level.dat"
}
step "downloading a world backup (running)"
backup_has_world

step "stop keeps the world, start brings it back"
api POST "/servers/$ID/stop" >/dev/null
wait_state stopped
kubectl -n minecraft get pvc mc-e2e-data >/dev/null || fail "volume gone after stop"

step "downloading a world backup (stopped, through a helper pod)"
backup_has_world
kubectl -n minecraft get pod mc-e2e-files >/dev/null || fail "no helper pod"

api POST "/servers/$ID/start" >/dev/null
wait_state running
kubectl -n minecraft get pod mc-e2e-files >/dev/null 2>&1 && fail "helper pod not removed on start"

step "auto sleep: idle server goes to sleep"
SETTINGS=$(api GET "/servers/$ID" | json 'json.dumps({**d["settings"], "auto_sleep": True})')
api PATCH "/servers/$ID" "{\"settings\":$SETTINGS}" >/dev/null
for _ in $(seq 60); do [ "$(state)" = sleeping ] && break; sleep 5; done
[ "$(state)" = sleeping ] || fail "server did not fall asleep"

step "auto sleep: a player join wakes it up"
python3 - "$MC_PORT" <<'PY'
# Minimal Minecraft handshake (next state: login) plus login start, enough for mc-router to wake the server.
import socket, struct, sys, uuid
def varint(n):
    out = b""
    while True:
        b = n & 0x7F
        n >>= 7
        out += bytes([b | (0x80 if n else 0)])
        if not n:
            return out
def string(s):
    b = s.encode()
    return varint(len(b)) + b
def packet(pid, data):
    body = varint(pid) + data
    return varint(len(body)) + body
s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=10)
s.sendall(packet(0, varint(772) + string("e2e.mine.test") + struct.pack(">H", 25565) + varint(2)))
s.sendall(packet(0, string("e2e_player") + uuid.uuid4().bytes))
try:
    s.recv(1024)
except Exception:
    pass
s.close()
PY
wait_state running

step "deleting"
api DELETE "/servers/$ID" '{"confirm":"e2e","keep_data":false}' >/dev/null
sleep 3
[ -z "$(kubectl -n minecraft get sts,svc,secret,pvc -l app.kubernetes.io/managed-by=mc-manager -o name)" ] || fail "resources left behind"

echo "e2e passed"
