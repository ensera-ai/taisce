#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# The first failover measurement: on a kind cluster with the CloudNativePG operator, install
# the chart at its highly available default (small resources, no model), kill the PostgreSQL
# primary, and time how long the API answers nothing. The number is for the machine that runs
# this and is written down beside its name; it is not a property of the chart. No model is
# configured, so no worker runs: a worker with nothing to form reports itself not ready, which is
# right, and the measurement is of the API's read and write path across the primary's loss.
#
#   bash deploy/helm/failover.sh            # creates kind cluster "taisce-failover", measures, leaves it
#   KEEP=0 bash deploy/helm/failover.sh     # and deletes the cluster afterwards
set -euo pipefail
cd "$(dirname "$0")/../.."
cluster=taisce-failover
image=ghcr.io/ensera-ai/taisce:failover
cnpg=${CNPG_MANIFEST:-https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.27/releases/cnpg-1.27.0.yaml}
results=${RESULTS:-results/failover}
mkdir -p "$results"
log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" | tee -a "$results/failover.log"; }

log "building $image"
docker build -q -t "$image" . > /dev/null
if ! kind get clusters | grep -qx "$cluster"; then
  log "creating kind cluster $cluster with three nodes"
  cat <<'KIND' | kind create cluster --name "$cluster" --config - > /dev/null
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes: [{role: control-plane}, {role: worker}, {role: worker}]
KIND
fi
kubectl config use-context "kind-$cluster" > /dev/null
kind load docker-image "$image" --name "$cluster" > /dev/null
log "installing the CloudNativePG operator"
kubectl apply --server-side -f "$cnpg" > /dev/null
kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=300s > /dev/null
# A measurement starts from a fresh install, never an upgrade: the bootstrap Job prints the first
# credential once, on the revision that minted it, and a repeat would find the project already
# reachable and mint nothing — so the token this script needs exists only on a first install. A fresh
# namespace also means the number measures the chart's default topology and not whatever an earlier
# run left behind.
if kubectl get namespace taisce > /dev/null 2>&1; then
  log "removing the previous release so the measurement starts from a first install"
  helm -n taisce uninstall memory --wait --timeout 10m > /dev/null 2>&1 || true
  kubectl delete namespace taisce --wait=true --timeout=10m > /dev/null
fi
log "installing the chart at its default topology, small resources, no model"
helm upgrade --install memory deploy/helm/taisce -n taisce --create-namespace --wait --timeout 20m \
  --set image.repository="${image%:*}" --set image.tag="${image##*:}" --set image.pullPolicy=Never \
  --set postgresql.cnpg.storage.size=2Gi --set postgresql.cnpg.sharedMemory=256Mi \
  --set postgresql.cnpg.resources.requests.cpu=250m --set postgresql.cnpg.resources.requests.memory=512Mi --set postgresql.cnpg.resources.limits.memory=2Gi \
  --set postgresql.cnpg.parameters.shared_buffers=128MB --set postgresql.cnpg.parameters.effective_cache_size=512MB --set postgresql.cnpg.parameters.maintenance_work_mem=64MB --set postgresql.cnpg.parameters.max_wal_size=1GB \
  --set api.requireFormation=false --set worker.replicas=0 > "$results/helm-install.log" 2>&1
kubectl -n taisce get pods -o wide | tee "$results/pods-before.txt"
revision="$(helm -n taisce list -o json | python3 -c 'import sys,json; print([r for r in json.load(sys.stdin) if r["name"]=="memory"][0]["revision"])')"
kubectl -n taisce wait --for=condition=complete "job/memory-taisce-bootstrap-${revision}" --timeout=600s > /dev/null
# Bootstrap prints two tokens: the operator's, which opens the management surface and no project,
# and the project's. The probe below walks through the project door, so it is the line that says
# "token:" and not the one that says "operator token:".
token="$(kubectl -n taisce logs "job/memory-taisce-bootstrap-${revision}" | grep -E '^token: tsk_' | head -1 | awk '{print $2}')"
test -n "$token"
kubectl -n taisce rollout status deployment/memory-taisce-api --timeout=600s > /dev/null
# The port-forward is the laptop's path to the service; it dies when the endpoints it forwards to
# The port-forward is supervised, not probed. It targets the service, and the service drops an API
# replica from its endpoints the moment readiness fails — which is what a caller sees, and therefore
# part of what is being measured — so the forward dies during a failover and must come back
# immediately. Restarting it inside the probe, with a sleep to let it settle, made the loop sample at
# the cost of its own repair: two seconds a tick, and an outage reported at the resolution of the
# instrument rather than of the system.
( while :; do kubectl -n taisce port-forward svc/memory-taisce-api 18080:8080 > /dev/null 2>&1; sleep 0.05; done ) &
supervisor=$!
trap 'kill "$supervisor" 2>/dev/null; pkill -P "$supervisor" 2>/dev/null; pkill -f "port-forward svc/memory-taisce-api" 2>/dev/null; true' EXIT
# A connection refused is a failed probe, because that is what the caller gets.
probe() { curl -s -o /dev/null -m 1 -w '%{http_code}' -H "Authorization: Bearer $token" http://127.0.0.1:18080/v1/freshness 2>/dev/null || echo 000; }
current_primary() { kubectl -n taisce get pods -l cnpg.io/cluster=memory-taisce-db,cnpg.io/instanceRole=primary -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true; }
until [ "$(probe)" = 200 ]; do sleep 1; done
log "API answers; observing one turn so the log is not empty"
curl -s -o /dev/null -X POST -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d '{"data_subject_id":"failover","messages":[{"role":"user","content":"I work at Ensera."}]}' http://127.0.0.1:18080/v1/observations
api_restarts_before="$(kubectl -n taisce get pods -l app.kubernetes.io/component=api -o jsonpath='{range .items[*]}{.status.containerStatuses[0].restartCount}{" "}{end}')"
primary="$(current_primary)"
# The primary is killed, not asked to stop. A graceful delete is a switchover the operator
# coordinates — the old primary keeps serving while its replicas catch up, and the API never notices —
# which measures a planned operation and not a failure. A node that dies gives no notice, so neither
# does this; the measurement ends when a different pod carries the primary label and the API answers.
log "primary is $primary; killing it without notice and polling four times a second"
start=$(date +%s.%N)
kubectl -n taisce delete pod "$primary" --grace-period=0 --force --wait=false > /dev/null 2>&1
first_fail=""; last_fail=""; recovered=""; new_primary=""
# Only the probe runs in the loop. A `kubectl get` inside the tick took two and a half seconds of
# wall clock, which quantised the measurement to its own cost and reported a zero-second outage from
# a single failing sample; the control plane is asked once, afterwards, for a fact that does not
# change during the window. The tick is therefore the curl and its one-second ceiling, and the
# interval is recorded beside the number so a reader knows the resolution of what they are reading.
while :; do
  code="$(probe)"
  now=$(date +%s.%N)
  printf '%s %s\n' "$now" "$code" >> "$results/probes.txt"
  if [ "$code" != 200 ]; then last_fail=$now; [ -z "$first_fail" ] && first_fail=$now; fi
  if [ "$code" = 200 ] && [ -n "$first_fail" ]; then recovered=$now; break; fi
  if [ -z "$first_fail" ] && awk -v a="$now" -v b="$start" 'BEGIN{exit !(a-b>60)}'; then
    log "the API never stopped answering in the 60s after the primary was killed"; break; fi
  if awk -v a="$now" -v b="$start" 'BEGIN{exit !(a-b>600)}'; then log "no recovery within 600s"; break; fi
  sleep 0.2
done
# Asked once, after the fact: which instance carries the primary now. A promotion that has not
# landed by the time the API answers is a promotion the API did not need to wait for.
for _ in 1 2 3 4 5 6 7 8 9 10; do
  new_primary="$(current_primary)"
  [ -n "$new_primary" ] && break
  sleep 1
done
kubectl -n taisce get pods -o wide | tee "$results/pods-after.txt"
# Whether a database failover restarts the API replicas. The API exits when its database is
# unreachable rather than serving errors, so a long enough outage costs a restart and a backoff;
# whether this one did is a fact about the deployment, not a claim, and it is recorded either way.
api_restarts_after="$(kubectl -n taisce get pods -l app.kubernetes.io/component=api -o jsonpath='{range .items[*]}{.status.containerStatuses[0].restartCount}{" "}{end}')"
if [ -n "$recovered" ]; then
  # Downtime is the first failure to the first success after it: the window in which a caller got
  # nothing. Recovery is measured from the kill, which is the only moment an operator can point at.
  downtime=$(awk -v a="$recovered" -v b="$first_fail" 'BEGIN{printf "%.1f", a-b}')
  to_fail=$(awk -v a="$first_fail" -v b="$start" 'BEGIN{printf "%.1f", a-b}')
  since_kill=$(awk -v a="$recovered" -v b="$start" 'BEGIN{printf "%.1f", a-b}')
  samples=$(wc -l < "$results/probes.txt" | tr -d ' ')
  interval=$(awk 'NR==1{f=$1} END{if (NR>1) printf "%.2f", ($1-f)/(NR-1); else print "0"}' "$results/probes.txt")
  log "RESULT: ${downtime}s answering nothing, ${since_kill}s from killing $primary to answering on ${new_primary:-unknown}; ${samples} samples about ${interval}s apart"
  printf '{"downtime_seconds":%s,"seconds_from_kill_to_first_failure":%s,"seconds_from_kill_to_recovery":%s,"samples":%s,"sample_interval_seconds":%s,"old_primary":"%s","new_primary":"%s","machine":"%s","api_restarts_before":"%s","api_restarts_after":"%s"}\n' \
    "$downtime" "$to_fail" "$since_kill" "$samples" "$interval" "$primary" "${new_primary:-unknown}" "$(uname -mrs)" "$api_restarts_before" "$api_restarts_after" > "$results/result.json"
fi
if [ "${KEEP:-1}" = 0 ]; then kind delete cluster --name "$cluster"; fi
