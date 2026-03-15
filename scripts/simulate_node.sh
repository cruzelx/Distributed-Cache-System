#!/usr/bin/env bash
# simulate_node.sh — simulate aux node removal and recovery on EKS
#
# Usage:
#   ./scripts/simulate_node.sh pod       # soft kill: delete pod, let K8s reschedule
#   ./scripts/simulate_node.sh ec2       # hard kill: terminate the underlying EC2 instance
#   ./scripts/simulate_node.sh add-local # add a 4th aux node to the local Docker Compose stack
#
# Prerequisites: kubectl configured, AWS CLI configured (for ec2 mode)

set -euo pipefail

NAMESPACE="cache"
TARGET_POD="aux-1"
TEST_KEY="sim-test-$(date +%s)"
TEST_VALUE="still-alive"
ENDPOINT="${ENDPOINT:-}"

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }
wait_pod_ready() {
    local pod=$1
    info "Waiting for $pod to be Ready..."
    kubectl wait pod "$pod" -n "$NAMESPACE" --for=condition=Ready --timeout=300s
}

require_endpoint() {
    if [[ -z "$ENDPOINT" ]]; then
        # Try to resolve from kubectl
        ENDPOINT=$(kubectl get svc nginx -n "$NAMESPACE" \
            -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)
        [[ -n "$ENDPOINT" ]] && ENDPOINT="http://$ENDPOINT" || \
            die "Set ENDPOINT env var, e.g. ENDPOINT=http://localhost:8080"
    fi
}

seed_key() {
    info "Writing test key '$TEST_KEY'..."
    curl -sf -X POST "$ENDPOINT/data" \
        -H "Content-Type: application/json" \
        -d "{\"key\":\"$TEST_KEY\",\"value\":\"$TEST_VALUE\"}" > /dev/null
    echo "   key=$TEST_KEY value=$TEST_VALUE"
}

read_key() {
    info "Reading back '$TEST_KEY'..."
    local resp
    resp=$(curl -sf "$ENDPOINT/data/$TEST_KEY" || echo '{"error":"request failed"}')
    echo "   response: $resp"
    if echo "$resp" | grep -q "\"$TEST_VALUE\""; then
        echo "   PASS — key survived"
    else
        echo "   FAIL — key missing or wrong value"
        return 1
    fi
}

# ---------------------------------------------------------------------------
# mode: pod — graceful pod delete, K8s reschedules immediately
# ---------------------------------------------------------------------------
mode_pod() {
    require_endpoint
    info "--- Soft pod-kill simulation ---"
    info "Current pods:"
    kubectl get pods -n "$NAMESPACE" -o wide | grep aux

    seed_key
    read_key

    info "Deleting pod $TARGET_POD..."
    kubectl delete pod "$TARGET_POD" -n "$NAMESPACE"

    info "Reading key during reschedule (expect success from replica)..."
    sleep 2
    read_key

    wait_pod_ready "$TARGET_POD"
    info "Pod is back. Final read:"
    read_key

    info "Done."
}

# ---------------------------------------------------------------------------
# mode: ec2 — hard EC2 termination
# ---------------------------------------------------------------------------
mode_ec2() {
    require_endpoint
    info "--- Hard EC2 termination simulation ---"

    # Find which node is running the target pod
    local node
    node=$(kubectl get pod "$TARGET_POD" -n "$NAMESPACE" -o jsonpath='{.spec.nodeName}')
    info "Pod $TARGET_POD is on node: $node"

    # Resolve node's private DNS to an instance ID
    local private_dns
    private_dns=$(kubectl get node "$node" -o jsonpath='{.status.addresses[?(@.type=="InternalDNS")].address}')
    info "Private DNS: $private_dns"

    local instance_id
    instance_id=$(aws ec2 describe-instances \
        --filters "Name=private-dns-name,Values=$private_dns" \
        --query 'Reservations[0].Instances[0].InstanceId' \
        --output text)
    [[ "$instance_id" == "None" || -z "$instance_id" ]] && die "Could not resolve instance ID"
    info "Instance ID: $instance_id"

    seed_key
    read_key

    echo ""
    echo "About to TERMINATE EC2 instance $instance_id ($node)."
    read -r -p "Type 'yes' to continue: " confirm
    [[ "$confirm" == "yes" ]] || { echo "Aborted."; exit 0; }

    aws ec2 terminate-instances --instance-ids "$instance_id" > /dev/null
    info "Instance terminated. Watching pod status..."
    echo "(EBS reattach may take 3-5 min if the volume needs force-detach)"
    echo ""

    # Poll until pod is running again
    local elapsed=0
    while true; do
        local phase
        phase=$(kubectl get pod "$TARGET_POD" -n "$NAMESPACE" \
            -o jsonpath='{.status.phase}' 2>/dev/null || echo "Pending")
        local node_now
        node_now=$(kubectl get pod "$TARGET_POD" -n "$NAMESPACE" \
            -o jsonpath='{.spec.nodeName}' 2>/dev/null || echo "unscheduled")
        printf "  [%3ds] phase=%-12s node=%s\n" "$elapsed" "$phase" "$node_now"
        [[ "$phase" == "Running" ]] && break
        sleep 15
        elapsed=$((elapsed + 15))
        [[ $elapsed -gt 600 ]] && die "Pod did not recover within 10 minutes"
    done

    info "Pod recovered. Reading key:"
    read_key
    info "Done."
}

# ---------------------------------------------------------------------------
# mode: add-local — scale up local Docker Compose aux nodes
# ---------------------------------------------------------------------------
mode_add_local() {
    local compose_file
    compose_file=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)/docker-compose.yml

    info "--- Adding aux node to local Docker Compose stack ---"
    info "Scaling aux service to 4 replicas..."
    docker compose -f "$compose_file" up -d --scale aux=4

    info "Waiting for new container to be healthy..."
    sleep 5
    docker compose -f "$compose_file" ps aux

    info "Verifying rebalance — reading 20 keys:"
    for i in $(seq 1 20); do
        local key="rebalance-$i"
        local resp
        resp=$(curl -sf "http://localhost:8080/data/$key" 2>/dev/null || echo '{}')
        printf "  key=%-15s %s\n" "$key" "$resp"
    done
    info "Done."
}

# ---------------------------------------------------------------------------
# entrypoint
# ---------------------------------------------------------------------------
case "${1:-}" in
    pod)       mode_pod ;;
    ec2)       mode_ec2 ;;
    add-local) mode_add_local ;;
    *)
        echo "Usage: $0 <pod|ec2|add-local>"
        echo ""
        echo "  pod        Delete aux-1 pod; K8s reschedules it immediately"
        echo "  ec2        Terminate the EC2 node running aux-1 (hard failure)"
        echo "  add-local  Scale local Docker Compose aux service to 4 replicas"
        echo ""
        echo "Environment:"
        echo "  ENDPOINT   Override the cache endpoint (default: auto-detect from kubectl)"
        exit 1
        ;;
esac
