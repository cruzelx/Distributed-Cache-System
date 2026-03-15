# Distributed Cache System

A distributed, in-memory key-value cache built in Go. It uses consistent hashing to spread data across a pool of cache nodes, a primary/standby master pair for coordinator fault tolerance, and a configurable replication factor so any single cache node can die without data loss.

---

## Table of Contents

- [Architecture](#architecture)
- [Components](#components)
- [Consistent Hashing](#consistent-hashing)
- [Replication](#replication)
- [Master Failover](#master-failover)
- [Data Flow](#data-flow)
- [Recovery Scenarios](#recovery-scenarios)
- [API Reference](#api-reference)
- [Configuration](#configuration)
- [Running Locally](#running-locally)
- [Deploying to AWS](#deploying-to-aws)
- [Simulating Node Failure and Addition](#simulating-node-failure-and-addition)
- [Load Testing](#load-testing)
- [Observability](#observability)
- [Trade-offs and Limitations](#trade-offs-and-limitations)

---

## Architecture

```
                        ┌─────────────────────────────────────┐
                        │             Clients                  │
                        └──────────────┬──────────────────────┘
                                       │
                        ┌──────────────▼──────────────────────┐
                        │          Nginx (port 8080)           │
                        │  GETs → both masters (round-robin)  │
                        │  writes → primary only              │
                        └──────┬───────────────┬──────────────┘
                               │               │
               ┌───────────────▼──┐     ┌──────▼────────────────┐
               │  Master Primary  │────▶│   Master Standby      │
               │   (port 8000)    │     │   (port 8000)         │
               │                  │     │                        │
               │  - hash ring     │     │  - mirrors ring state  │
               │  - health checks │     │  - serves reads        │
               │  - rebalance     │     │  - promotes on failure │
               └──────┬───────────┘     └───────────────────────┘
                      │
          ┌───────────┼───────────┐
          │           │           │
    ┌─────▼──┐  ┌─────▼──┐  ┌────▼───┐
    │  aux1  │  │  aux2  │  │  aux3  │
    │  LRU   │  │  LRU   │  │  LRU   │
    │  cache │  │  cache │  │  cache │
    └────────┘  └────────┘  └────────┘
```

The system has three tiers:

1. **Nginx** — routes writes to the primary master, load-balances reads across both masters
2. **Master layer** — stateless coordinators that own the consistent hash ring and proxy requests to the right cache nodes
3. **Aux layer** — the actual cache nodes, each running an LRU cache backed by a doubly-linked list + hashmap

---

## Components

### Master Server

The master does not store any data itself. Its job is:

- Maintain the **consistent hash ring** — knows which aux node is responsible for each key
- **Proxy requests** to the correct aux node(s)
- **Health check** all aux nodes every 5 seconds and update the ring when nodes go up or down
- Push **ring update events** to the standby so it stays in sync
- **Rebalance** keys when a node is added or removed

There are always two master instances: a **primary** and a **standby**. The primary handles all writes. The standby mirrors ring state and handles reads. If the primary dies, the standby promotes itself automatically.

### Auxiliary (Aux) Server

Each aux server is an independent cache node. It stores key-value pairs in an **LRU (Least Recently Used)** cache. The LRU is implemented using a doubly-linked list (for O(1) eviction) and a hashmap (for O(1) lookup).

Each aux node:
- Stores its cache shard in memory
- Persists to disk every 10 seconds (gob-encoded) for crash recovery
- Loads from disk on startup
- Runs a background **reaper** goroutine that sweeps expired keys every 30 seconds
- Sends all its key-value mappings to the master before graceful shutdown so they can be redistributed

### Nginx

Acts as the entry point. Two upstream pools are defined:

- `masters_write` — primary only (standby is failover)
- `masters_read` — both masters, round-robin

A `map` directive routes traffic:

```nginx
map "$request_method$request_uri" $upstream_pool {
    ~^POST/data/bulk/get  masters_read;   # bulk get is a read
    ~^GET                 masters_read;
    default               masters_write;
}
```

This effectively doubles read throughput because the standby is not sitting idle — it actively handles half of all GET requests.

---

## Consistent Hashing

Keys are distributed across aux nodes using a **consistent hash ring** with 150 virtual nodes per physical node. The ring is a sorted array of `uint32` hashes.

**Why 150 virtual nodes?**

With only 3 physical nodes and a small number of virtual nodes (say 3), the ring has very few points and key distribution can be uneven — one node might hold 60% of keys while another holds 20%. At 150 virtual nodes per physical node (450 total ring points with 3 servers), the statistical variance in key distribution becomes small.

**How a key is routed:**

```
1. Hash the key with CRC32:  hash("hello") → 2134564789
2. Binary search the sorted ring for the first position >= that hash
3. The physical node at that position is the primary node
4. Walk clockwise for R-1 more distinct physical nodes (replicas)
```

**Ring operations:**

| Operation | Lock | Complexity |
|-----------|------|------------|
| `AddNode` | Write lock | O(V log V) — re-sort after inserting V vnodes |
| `RemoveNode` | Write lock | O(N) — scan and filter sorted array |
| `GetNode` | Read lock | O(log N) — binary search |
| `GetNodes` | Read lock | O(N) worst case — walk ring for R distinct nodes |

The ring uses `sync.RWMutex` so concurrent reads (`GetNode`/`GetNodes`) never block each other. Only topology changes (`AddNode`/`RemoveNode`) take an exclusive lock.

**What happens when a node is removed:**

The keys that were assigned to the dead node get remapped to the next node clockwise. Only the "slice" of the ring that belonged to the dead node is affected — all other key assignments stay the same. This is the core benefit of consistent hashing over naive modular hashing.

```
Before (aux2 dies):       After:
  0 → aux1                  0 → aux1
  1 → aux2   ← dead    →   1 → aux3  (next clockwise)
  2 → aux3                  2 → aux3
```

---

## Replication

Each key is written to `REPLICATION_FACTOR` distinct aux nodes (default: 2). The replicas are determined by walking clockwise on the ring from the key's hash position and collecting the first N distinct physical nodes.

```
Key "hello" hashes near position X on the ring
GetNodes("hello", 2) → [aux3, aux1]

Write:  aux3 ← "hello"="world"    (primary)
        aux1 ← "hello"="world"    (replica)
```

**Write path** — fan-out, at-least-one semantics:

All replica writes fire in parallel goroutines. The master returns 200 as long as at least one write succeeds. If one replica is temporarily down, the write still lands on the other.

**Read path** — primary-first with fallback:

The master tries the primary node first. If it is unreachable or returns a non-200, it falls through to the secondary. The client sees a 200 either way.

```
GET "hello":
  try aux3 → unreachable (node is down)
  try aux1 → 200 {"key":"hello","value":"world"}
```

**Delete path** — remove from all replicas:

A DELETE is sent to all replica nodes. Returns 200 if at least one held the key. This prevents "ghost reads" where a deleted key re-appears from a surviving replica.

**Key insight — no dedicated replica servers:**

Instead of pairing each aux node with a dedicated standby, aux nodes serve as replicas for each other based on their ring positions. With 3 nodes and RF=2:

```
        aux1    aux2    aux3
keyA     ✓               ✓     (aux2 never sees keyA)
keyB     ✓       ✓             (aux3 never sees keyB)
keyC             ✓       ✓     (aux1 never sees keyC)
```

Each node holds roughly 2/3 of all keys. The system tolerates any single node dying without data loss. To survive 2 simultaneous failures, set `REPLICATION_FACTOR=3`.

---

## Master Failover

The standby monitors the primary by polling `/health` every 5 seconds. After 3 consecutive failures (~15 seconds), it promotes itself to primary:

```
Primary: RUNNING ──────────────────────── DEAD
Standby: monitoring → monitoring → monitoring → PROMOTES → now primary
```

**Promotion steps:**
1. Standby sets `isPrimary = true`
2. Starts running `HealthCheck` on aux nodes (was previously passive)
3. Nginx detects the primary is down via `max_fails=3 fail_timeout=15s` and stops routing to it
4. All traffic now flows to the standby, which is now the sole master

**Split-brain prevention:**

If the original primary restarts after the standby has promoted, it checks the standby's `/role` endpoint before assuming the primary role. If the standby reports `"primary"`, the restarting node demotes itself to standby and begins monitoring the now-primary standby:

```go
if standby != "" && checkStandbyRole(client, standby) == "primary" {
    // the standby already promoted — we become the new standby
    m.isPrimary.Store(false)
    go m.monitorPrimary(standby, ...)
}
```

This prevents two nodes simultaneously running health checks and issuing conflicting rebalance decisions.

**Ring state sync:**

The primary pushes a `RingUpdate{action, aux}` event to the standby over `/ring-update` every time a node is added or removed. The standby applies these in `RingUpdateHandler` and keeps its ring identical to the primary's. When the standby serves reads, it routes to the same aux nodes the primary would have chosen.

---

## Data Flow

### Write (PUT)

```
1. Client sends POST /data {"key":"x","value":"y","ttl":60}
2. Nginx routes to primary master (writes always go to primary)
3. Master calls GetNodes("x", replicationFactor) → [aux2, aux3]
4. Master fans out in parallel:
     POST aux2/data {"key":"x","value":"y","ttl":60}
     POST aux3/data {"key":"x","value":"y","ttl":60}
5. Both aux nodes store the key in their LRU cache with an expiry timestamp
6. Master returns 200 when at least one write succeeds
```

### Read (GET)

```
1. Client sends GET /data/x
2. Nginx routes to either master (reads are load-balanced)
3. Master calls GetNodes("x", replicationFactor) → [aux2, aux3]
4. Master tries aux2 first:
     GET aux2/data/x → 200 {"key":"x","value":"y"}
5. Returns response to client immediately (aux3 never contacted)
```

### TTL expiry

```
On write:  expiry["x"] = time.Now().Add(60 * time.Second)
On read:   if time.Now().After(expiry["x"]) → delete and return 404
Every 30s: reaper goroutine sweeps all keys, deletes expired ones
On disk:   expired keys are skipped when loading from disk on startup
```

### Node failure and rebalance

```
1. aux2 dies
2. HealthCheck detects failure → handleDeadAuxServer("aux2")
3. aux2 removed from ring → primary pushes ring-update("remove","aux2") to standby
4. Future writes to keys that were on aux2 now route to aux3 (next clockwise)
5. If aux2 shutdown gracefully: it POSTs all its mappings to /rebalance-dead-aux
   → master re-PUTs each key to its new location (with full replication fan-out)
6. If aux2 crashed hard: replicas on aux3 already hold the data (RF=2)
```

### Dynamic node addition

```
1. Operator spins up a new aux container
2. POST /nodes {"addr":"aux4:3004"}
3. Master verifies aux4 is reachable (health check)
4. aux4 added to ring and activeAuxServers
5. Ring-update("add","aux4") pushed to standby
6. For each ring neighbor of aux4: fetch their mappings and re-PUT
   (keys that now belong to aux4 land on aux4 through normal rebalance)
7. New health monitoring goroutine started for aux4
```

---

## Recovery Scenarios

| Scenario | What happens |
|---|---|
| Single aux node crashes (hard) | RF=2 means all keys have a surviving replica. No data loss, no rebalance needed. |
| Single aux node crashes (graceful) | Sends mappings to master before dying. Master rebalances to remaining nodes. |
| Single aux node restarts | Loads cache from disk. Master detects it as alive, triggers rebalance of neighboring keys. |
| All aux nodes restart | Each loads from disk. Master's backup file used to restore anything not on disk. |
| Primary master dies | Standby promotes after ~15s. Nginx routes all traffic to standby. No data loss (data is in aux nodes). |
| Primary master restarts | Checks standby's role. If standby promoted, original primary demotes itself to standby. |
| Both masters die | Cache nodes still hold data. System resumes when either master restarts. |

---

## API Reference

All endpoints are available through Nginx on port 8080.

### Single key operations

```bash
# Write a key (optional TTL in seconds; omit for no expiry)
POST /data
{"key": "user:123", "value": "alice", "ttl": 300}

# Read a key
GET /data/{key}
→ {"key": "user:123", "value": "alice"}

# Delete a key
DELETE /data/{key}
→ 200 OK  /  404 if not found
```

### Bulk operations

```bash
# Write multiple keys in one request
POST /data/bulk
[
  {"key": "a", "value": "apple"},
  {"key": "b", "value": "banana", "ttl": 60},
  {"key": "c", "value": "cherry"}
]

# Read multiple keys — missing/expired keys are silently omitted
POST /data/bulk/get
["a", "b", "c", "missing"]
→ {"a":"apple","b":"banana","c":"cherry"}
```

### Cluster management

```bash
# Add a new aux node to the ring at runtime
POST /nodes
{"addr": "aux4:3004"}

# Health check
GET /health
→ 200 OK

# Which role is this master?
GET /role
→ {"role": "primary"}  or  {"role": "standby"}

# Current ring state (which aux nodes are active)
GET /state
→ {"aux1:3001": true, "aux2:3002": true, "aux3:3003": false}
```

### Aux server (direct, for debugging)

```bash
# The aux servers are exposed on ports 9001-9004
GET  http://localhost:9001/health
GET  http://localhost:9001/mappings     # dump all key-value pairs
DELETE http://localhost:9001/erase      # clear the entire cache
```

---

## Configuration

All configuration is via environment variables.

### Master

| Variable | Default | Description |
|---|---|---|
| `ROLE` | `primary` | `primary` or `standby` |
| `PORT` | — | Port to listen on |
| `STANDBY_SERVER` | — | Address of standby (primary only) |
| `PRIMARY_MASTER` | — | Address of primary to monitor (standby only) |
| `AUX_SERVERS` | — | Comma-separated list of aux addresses |
| `REPLICATION_FACTOR` | `2` | How many aux nodes each key is written to |

### Auxiliary

| Variable | Default | Description |
|---|---|---|
| `PORT` | — | Port to listen on |
| `ID` | — | Unique identifier (used for disk persistence filename) |
| `MASTER_SERVER` | — | Master address (used on graceful shutdown to send mappings) |
| `LRU_CAPACITY` | `128` | Maximum number of keys this node holds in memory |

---

## Running Locally

**Prerequisites:** Docker, Docker Compose

```bash
git clone https://github.com/cruzelx/Distributed-Cache-System.git
cd Distributed-Cache-System

# Start the full stack (nginx + 2 masters + 3 aux nodes + prometheus + grafana)
docker compose up --build

# Write a key
curl -X POST http://localhost:8080/data \
  -H "Content-Type: application/json" \
  -d '{"key":"hello","value":"world"}'

# Read it back
curl http://localhost:8080/data/hello

# Write with TTL (expires in 10 seconds)
curl -X POST http://localhost:8080/data \
  -H "Content-Type: application/json" \
  -d '{"key":"temp","value":"gone-soon","ttl":10}'

# Bulk write
curl -X POST http://localhost:8080/data/bulk \
  -H "Content-Type: application/json" \
  -d '[{"key":"a","value":"1"},{"key":"b","value":"2"},{"key":"c","value":"3"}]'

# Bulk read
curl -X POST http://localhost:8080/data/bulk/get \
  -H "Content-Type: application/json" \
  -d '["a","b","c"]'

# Add a new cache node at runtime
docker compose up -d aux4
curl -X POST http://localhost:8080/nodes \
  -H "Content-Type: application/json" \
  -d '{"addr":"aux4:3004"}'

# Simulate a cache node crash and verify replica coverage
docker kill distributed-cache-system-aux1-1
curl http://localhost:8080/data/hello   # still returns 200 — served from replica
```

---

## Deploying to AWS

The `infrastructure/` directory contains everything needed to deploy the full stack to AWS EKS in `ap-southeast-2` (Sydney) using Terraform and Kubernetes.

**Prerequisites**

- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.5
- [AWS CLI](https://aws.amazon.com/cli/) configured (`aws configure`)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Docker](https://www.docker.com/) with buildx support

**What gets provisioned**

| Resource | Detail |
|---|---|
| VPC | 3 public + 3 private subnets across 3 AZs, single NAT gateway |
| EKS cluster | K8s 1.31, 3x `t3.medium` worker nodes |
| ECR | Two private image repositories (`master`, `auxiliary`) |
| EBS CSI driver | Enables per-pod persistent volumes for aux nodes |
| S3 + DynamoDB | Remote Terraform state and lock table |

**Estimated cost:** ~$230/month (EKS control plane + 3x t3.medium + NAT gateway). Run `make destroy` when not in use.

---

### Step 1 — Bootstrap remote state

Creates the S3 bucket and DynamoDB table that Terraform uses to store its own state. Run this **once**.

```bash
cd infrastructure
make bootstrap
```

Copy the `state_bucket_name` output into `backend.tf`:

```hcl
backend "s3" {
  bucket = "distributed-cache-tfstate-xxxxxxxx"   # ← paste here
  ...
}
```

---

### Step 2 — Provision infrastructure

```bash
make init    # initialise Terraform with the remote backend
make plan    # preview what will be created (63 resources)
make apply   # provision VPC, EKS cluster, ECR (~15 minutes)
```

After `apply` completes, outputs are printed:

```
cluster_endpoint  = "https://xxxx.gr7.ap-southeast-2.eks.amazonaws.com"
ecr_master_url    = "123456789.dkr.ecr.ap-southeast-2.amazonaws.com/distributed-cache/master"
ecr_auxiliary_url = "123456789.dkr.ecr.ap-southeast-2.amazonaws.com/distributed-cache/auxiliary"
configure_kubectl = "aws eks update-kubeconfig --region ap-southeast-2 --name distributed-cache"
```

---

### Step 3 — Connect kubectl

```bash
make connect
# or directly:
aws eks update-kubeconfig --region ap-southeast-2 --name distributed-cache

kubectl get nodes   # should show 3 Ready nodes
```

---

### Step 4 — Build and push images

Builds both Docker images for `linux/amd64` (required for EKS x86_64 nodes even if you are on Apple Silicon) and pushes them to ECR.

```bash
make build-push
```

To push a versioned tag instead of `latest`:

```bash
make build-push TAG=v1.0.0
```

---

### Step 5 — Deploy to Kubernetes

Applies all manifests in dependency order: namespace → storage class → config → masters → aux StatefulSet → nginx → monitoring.

```bash
make deploy
```

Watch pods come up:

```bash
kubectl get pods -n cache -w
```

All pods should reach `Running` within ~3 minutes. The aux StatefulSet pods (`aux-0`, `aux-1`, `aux-2`) may take slightly longer — they wait for EBS volumes to be provisioned and attached.

---

### Step 6 — Verify

Get the public endpoint:

```bash
kubectl get svc nginx -n cache
# EXTERNAL-IP column shows the NLB DNS name
```

Smoke test:

```bash
ENDPOINT="http://<NLB-DNS>"

curl -X POST $ENDPOINT/data \
  -H "Content-Type: application/json" \
  -d '{"key":"hello","value":"world"}'

curl $ENDPOINT/data/hello
# → {"key":"hello","value":"world"}
```

---

### Tear down

```bash
make destroy   # prompts for confirmation, then destroys all AWS resources
```

Note: EBS PersistentVolumes use `reclaimPolicy: Retain` — they survive `make destroy`. Delete them manually in the AWS console or with:

```bash
kubectl delete pvc --all -n cache
```

---

## Simulating Node Failure and Addition

These steps work against both the local Docker Compose stack and the live EKS deployment. Commands below use the EKS endpoint — replace with `http://localhost:8080` for local testing.

### Simulate a cache node crash

```bash
ENDPOINT="http://<NLB-DNS>"

# Write a key that will be replicated across two aux nodes
curl -X POST $ENDPOINT/data \
  -H "Content-Type: application/json" \
  -d '{"key":"resilience-test","value":"still-alive"}'

# Hard-kill one aux pod (simulates an EC2/container crash)
kubectl delete pod aux-1 -n cache

# Key is still readable — served from the replica on aux-0 or aux-2
curl $ENDPOINT/data/resilience-test
# → {"key":"resilience-test","value":"still-alive"}

# K8s reschedules aux-1 automatically — watch it come back
kubectl get pods -n cache -w
```

### Simulate a full EC2 node failure

```bash
# Find which EC2 instance is running the pod you want to kill
kubectl get pods -n cache -o wide

# Get the instance ID for that node
aws ec2 describe-instances \
  --filters "Name=private-dns-name,Values=<node-private-dns>" \
  --query "Reservations[0].Instances[0].InstanceId" \
  --output text

# Terminate it hard (no graceful shutdown)
aws ec2 terminate-instances --instance-ids <instance-id>

# Watch the cascade: pods go Pending → ContainerCreating → Running
# EKS auto-scaler provisions a replacement node in the same AZ
kubectl get pods -n cache -w

# EBS volumes reattach automatically to the new node (~3-5 minutes)
# Data is preserved because:
#   1. The EBS volume (disk snapshot) survives EC2 termination
#   2. Keys on the dead node had replicas on surviving nodes (RF=2)
```

**What to expect:**

| Time | Event |
|---|---|
| T+0s | EC2 terminated |
| T+~25s | K8s marks node `NotReady`, evicts pods |
| T+~25s | nginx pod on dead node immediately replaced on a healthy node |
| T+~90s | EKS provisions a replacement EC2 in the same AZ |
| T+~3-5m | EBS volume force-detached from terminated instance, reattached to new node |
| T+~5m | All pods `Running`, system fully recovered |

### Simulate master failover

```bash
# Find and kill the primary master pod
kubectl delete pod -n cache -l app=master

# The standby detects 3 consecutive health check failures (~15s) and promotes
# Watch the standby logs for the promotion event
kubectl logs -n cache -l app=master-standby --follow | grep -i "promot"

# System continues serving requests throughout — verify with:
watch -n1 "curl -s $ENDPOINT/data/resilience-test"
```

### Add a new cache node at runtime

#### Locally (Docker Compose)

```bash
# Start the fourth aux container
docker compose up -d aux4

# Register it with the master — it joins the hash ring immediately
curl -X POST http://localhost:8080/nodes \
  -H "Content-Type: application/json" \
  -d '{"addr":"aux4:3004"}'

# Master fetches mappings from ring neighbours and rebalances keys to aux4
# Check the master logs to see the rebalance in progress
docker logs distributed-cache-system-master-1 --follow | grep -i "rebalanc"
```

#### On EKS

Scale the StatefulSet and register the new pod:

```bash
# Scale from 3 to 4 aux nodes
kubectl scale statefulset aux --replicas=4 -n cache

# Wait for aux-3 to be Running
kubectl get pods -n cache -w

# Get the new pod's cluster-internal address and register it
kubectl exec -n cache deploy/master -- \
  wget -qO- --post-data='{"addr":"aux-3.aux-headless.cache.svc.cluster.local:3000"}' \
  --header='Content-Type: application/json' \
  http://localhost:8000/nodes

# Also update the ConfigMap so future master restarts know about aux-3
kubectl edit configmap cache-config -n cache
# Update AUX_SERVERS to include aux-3.aux-headless.cache.svc.cluster.local:3000
```

### Verify rebalance after node change

```bash
# Write 20 keys before the change
for i in $(seq 1 20); do
  curl -s -X POST $ENDPOINT/data \
    -H "Content-Type: application/json" \
    -d "{\"key\":\"k$i\",\"value\":\"v$i\"}" > /dev/null
done

# Kill a node or add one here

# Verify all 20 keys are still readable
MISSING=0
for i in $(seq 1 20); do
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" $ENDPOINT/data/k$i)
  if [ "$STATUS" != "200" ]; then
    echo "MISSING: k$i"
    MISSING=$((MISSING+1))
  fi
done
echo "Missing keys: $MISSING / 20"
```

---

## Load Testing

Uses [Locust](https://locust.io/) to drive realistic mixed workloads. The test file is at `load_test/locust.py`.

**Install Locust:**

```bash
pip install locust
```

**Run against the live EKS endpoint:**

```bash
cd load_test

locust -f locust.py \
  --host http://<NLB-DNS> \
  --headless -u 100 -r 10 --run-time 2m \
  --html ../load_test_report.html
```

**Run with the interactive web UI** (opens at `http://localhost:8089`):

```bash
locust -f locust.py --host http://<NLB-DNS>
```

**Run against the local Docker Compose stack:**

```bash
locust -f locust.py --host http://localhost:8080 \
  --headless -u 50 -r 5 --run-time 1m
```

**What the test simulates:**

| User type | Weight | Behaviour |
|---|---|---|
| `ReadHeavyUser` | 70% | 4 GETs per PUT across a 500-key pool — simulates a typical cache consumer |
| `WriteHeavyUser` | 20% | High write rate with unique keys + write-then-read consistency checks — simulates an ingestion pipeline |
| `BulkUser` | 10% | Bulk PUT and bulk GET with batches of 10-50 keys — exercises the shard batch-locking path |

**Observed results on 3x t3.medium EKS nodes (ap-southeast-2), 100 concurrent users:**

```
~620 req/s total throughput
p50:  50ms
p95:  77ms
p99: 120ms
0 failures across 69,683 requests
```

**Tuning parameters** (edit `locust.py` or pass via CLI):

| Parameter | CLI flag | Description |
|---|---|---|
| Concurrent users | `-u 100` | Peak number of simulated users |
| Ramp-up rate | `-r 10` | Users added per second during ramp-up |
| Duration | `--run-time 2m` | How long to sustain peak load |

---

## Observability

| Service | URL | What it shows |
|---|---|---|
| Prometheus | `http://localhost:9090` | Raw metrics scrape |
| Grafana | `http://localhost:4000` | Dashboards (default login: admin/admin) |
| Aux1 metrics | `http://localhost:9001/metrics` | Per-node request count and latency |
| Aux2 metrics | `http://localhost:9002/metrics` | |
| Aux3 metrics | `http://localhost:9003/metrics` | |

**Metrics exposed:**

- `master_request_total{method}` — total requests handled by the master, by HTTP method
- `master_response_time_seconds{method}` — latency histogram at the master layer
- `auxiliary_request_total{method}` — total requests handled per aux node
- `auxiliary_response_time_seconds{method}` — latency histogram at the aux layer

---

## Trade-offs and Limitations

### Consistency model

This system prioritizes **availability over consistency** (AP in CAP theorem terms).

**Stale reads are possible.** If a write to the secondary replica fails silently (network blip, node temporarily overloaded), the secondary holds an old value. A future read that hits the secondary returns the stale value. There is no read-repair or anti-entropy process.

**No strong consistency guarantees.** Two clients reading the same key from different masters may see different values in the brief window after a write if the secondary hasn't been updated yet.

This is the same trade-off made by Redis Cluster and DynamoDB. For a cache — where the source of truth is a database behind it — this is almost always acceptable.

### Replication is synchronous but not atomic

All replica writes fire in parallel and the master waits for all of them before returning. However, there is no two-phase commit. If the master crashes after writing to the primary replica but before writing to the secondary, the secondary is permanently stale until the key is written again.

### No quorum reads

Reads return the first successful response from any replica. There is no quorum check (e.g. "ask 2 out of 3 replicas and return the most recent value"). This keeps latency low but means a stale replica can serve an old value without the system detecting it.

### Hash ring changes cause temporary inconsistency

When a node is added or removed, the ring topology changes. Keys in the migrating range may be read from the old node (which still has them) while new writes go to the new node. The rebalance process closes this window by re-PUTting migrating keys, but there is a brief period of inconsistency during rebalance.

### Standby reads are eventually consistent

The standby receives ring updates asynchronously via `/ring-update`. In the window between a ring change on the primary and the update arriving at the standby, the standby might route a read to the wrong aux node. This window is typically milliseconds.

### LRU eviction loses data silently

When a cache node's LRU reaches capacity (`LRU_CAPACITY`), the least recently used key is evicted. There is no notification to the master and no redistribution to another node. The key simply disappears until it is written again.
