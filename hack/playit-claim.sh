#!/usr/bin/env bash
# Creates a self-managed playit.gg agent and prints a Secret with its key on stdout.
# Only self-managed agents may create tunnels, keys from the playit dashboard are read only.
# Usage: hack/playit-claim.sh [namespace] [secret name] | kubeseal --format yaml > playit_secret.yaml
set -euo pipefail
NS=${1:-minecraft}
NAME=${2:-kubecraft-playit}
API=${PLAYIT_API:-https://api.playit.gg}

post() { curl -sS -X POST "$API$1" -H 'Content-Type: application/json' -d "$2"; }
data() { python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("data") if d.get("status")=="success" else "ERR "+json.dumps(d))'; }

CODE=$(od -An -N5 -tx1 /dev/urandom | tr -d ' \n')
echo "Open this link while logged in to playit.gg and approve the agent:" >&2
echo "  https://playit.gg/claim/$CODE" >&2
while :; do
  S=$(post /claim/setup "{\"code\":\"$CODE\",\"agent_type\":\"self-managed\",\"version\":\"kubecraft\"}" | data)
  case "$S" in
    UserAccepted) break ;;
    UserRejected) echo "rejected in the browser" >&2; exit 1 ;;
    WaitingForUserVisit|WaitingForUser) sleep 3 ;;
    *) echo "claim failed: $S" >&2; exit 1 ;;
  esac
done
KEY=$(post /claim/exchange "{\"code\":\"$CODE\"}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["secret_key"])')
[ -n "$KEY" ] || { echo "no key returned" >&2; exit 1; }
echo "approved, agent created" >&2
cat <<YAML
apiVersion: v1
kind: Secret
metadata:
  name: $NAME
  namespace: $NS
type: Opaque
stringData:
  SECRET_KEY: "$KEY"
YAML
