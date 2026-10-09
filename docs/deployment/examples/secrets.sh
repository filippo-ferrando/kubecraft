#!/usr/bin/env bash
# Creates the sealed secrets kubecraft needs. Run from the homelab repo root with a
# kubeconfig for the cluster; writes files under infrastructure/kubecraft/.
set -euo pipefail

OUT=infrastructure/kubecraft
mkdir -p "$OUT"
seal() { kubeseal --controller-namespace kube-system --controller-name sealed-secrets --format yaml; }

# 1. Database login: same values in the database namespace (for the CNPG role)
#    and in the minecraft namespace (for the manager). The username must match the role.
DB_PW="$(openssl rand -base64 32 | tr -d '/+=')"
for ns in database minecraft; do
  kubectl create secret generic kubecraft-db-credentials \
    --namespace "$ns" \
    --from-literal=username=kubecraft \
    --from-literal=password="$DB_PW" \
    --dry-run=client -o yaml |
    kubectl label --local -f - cnpg.io/reload=true -o yaml |
    seal >"$OUT/db_secret_${ns}.yaml"
done

# 2. Session secret and the first admin. The admin must change the password at first login.
ADMIN_PW="$(openssl rand -base64 18)"
kubectl create secret generic kubecraft-secrets \
  --namespace minecraft \
  --from-literal=SESSION_SECRET="$(openssl rand -hex 32)" \
  --from-literal=BOOTSTRAP_ADMIN_USER=admin \
  --from-literal=BOOTSTRAP_ADMIN_PASSWORD="$ADMIN_PW" \
  --dry-run=client -o yaml | seal >"$OUT/secrets.yaml"

echo "Bootstrap admin password (store it, it is not shown again): $ADMIN_PW"
