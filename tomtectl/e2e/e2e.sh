#!/usr/bin/env bash
# End-to-end proof of the K2 llm path on a real kind cluster:
# build the runtime image, load it, deploy the in-cluster stub
# endpoint, store the key with `tomtectl set-key`, `tomtectl up` the
# llm agent, and assert (a) the model's reply reaches `tomtectl logs`
# and (b) a wrong key fails closed as a logged `wake failed`.
#
# Needs: Go, docker, kind (cluster running; KIND_CLUSTER overrides the
# name, KUBE_CONTEXT the kubeconfig context), kubectl. Cleans up its
# namespace unless E2E_KEEP=1.
set -euo pipefail
cd "$(dirname "$0")/.."

IMG=tomte-agent:0.2.0
NS=tomte-e2e
CLUSTER=${KIND_CLUSTER:-kind}
CTX=${KUBE_CONTEXT:-kind-$CLUSTER}

k() { kubectl --context "$CTX" "$@"; }
# tomtectl takes flags after the command
t() { local cmd=$1; shift; ./tomtectl "$cmd" --context "$CTX" "$@"; }

say() { printf '\n== %s\n' "$*"; }

wait_for_log() { # wait_for_log <pattern> <tries>
  local pattern=$1 tries=$2 out
  for _ in $(seq 1 "$tries"); do
    # stderr stays in $out so a CLI-level failure is diagnosable, not
    # an empty transcript.
    out=$(t logs -f e2e/agent.yaml -n "$NS" 2>&1 || true)
    if grep -q "$pattern" <<<"$out"; then
      grep "$pattern" <<<"$out" | head -n2
      return 0
    fi
    sleep 5
  done
  echo "did not see '$pattern' in agent logs; last output:" >&2
  echo "$out" >&2
  t status -f e2e/agent.yaml -n "$NS" >&2 || true
  return 1
}

cleanup() {
  if [ "${E2E_KEEP:-0}" != 1 ]; then
    say "cleaning up namespace $NS"
    k delete namespace "$NS" --ignore-not-found --wait=false >/dev/null
  fi
}
trap cleanup EXIT

say "building CLI and runtime image"
go build -o tomtectl .
docker build -q -t "$IMG" .
kind load docker-image "$IMG" --name "$CLUSTER"

say "creating namespace $NS and storing the key (stdin, never argv)"
k create namespace "$NS" --dry-run=client -o yaml | k apply -f - >/dev/null
printf 'sk-e2e-stub-key' | t set-key -f e2e/agent.yaml -n "$NS"

say "deploying the in-cluster stub endpoint"
k apply -n "$NS" -f e2e/stub.yaml >/dev/null
k rollout status -n "$NS" deploy/llm-stub --timeout=120s

say "tomtectl up"
t up -f e2e/agent.yaml -n "$NS"

say "waiting for the model's reply in tomtectl logs"
wait_for_log "TOMTE_STUB_OK" 24

say "negative: a wrong key must fail closed"
printf 'sk-wrong-key' | t set-key -f e2e/agent.yaml -n "$NS"
# Roll the agent so it picks up the changed Secret env; the stub keeps
# its old env and now rejects the agent's key.
t up -f e2e/agent.yaml -n "$NS"
wait_for_log "wake failed.*401" 24

say "PASS: llm path verified end to end (positive + fail-closed)"
