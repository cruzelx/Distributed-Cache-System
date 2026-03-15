"""
Distributed Cache Load Test
============================
Run against the live EKS endpoint:

  locust -f locust.py \
    --host <hosted-cache-endpoint> \
    --headless -u 100 -r 10 --run-time 2m

Or open the web UI (default localhost:8089) and configure interactively:
  locust -f locust.py --host <endpoint>

Three user types simulate a realistic workload mix:
  - ReadHeavyUser  (70% of users) — read-heavy, cache-hit focused
  - WriteHeavyUser (20% of users) — write-heavy, many unique keys
  - BulkUser       (10% of users) — bulk get/set operations
"""

import random
import string
from locust import HttpUser, task, between


# ---------------------------------------------------------------------------
# Shared key pool — large enough to cause evictions, small enough for hits
# ---------------------------------------------------------------------------
KEY_POOL = [f"key:{i}" for i in range(500)]
VALUE_CHARS = string.ascii_letters + string.digits


def random_value(length=32):
    return "".join(random.choices(VALUE_CHARS, k=length))


def random_key():
    return random.choice(KEY_POOL)


# ---------------------------------------------------------------------------
# Read-heavy user — simulates a typical cache consumer
# Reads 4x more than it writes; expects mostly cache hits.
# ---------------------------------------------------------------------------
class ReadHeavyUser(HttpUser):
    weight = 70
    wait_time = between(0.05, 0.2)

    @task(4)
    def get(self):
        key = random_key()
        with self.client.get(
            f"/data/{key}",
            name="/data/[key]",
            catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            elif resp.status_code == 404:
                # Cache miss is not a failure — it's expected behaviour
                resp.success()
            else:
                resp.failure(f"Unexpected status {resp.status_code}")

    @task(1)
    def put(self):
        key = random_key()
        self.client.post(
            "/data",
            json={
                "key": key,
                "value": random_value(),
                "ttl": random.choice([0, 30, 60, 300]),
            },
            name="/data PUT",
        )

    @task(1)
    def put_with_ttl(self):
        """Write a short-lived key to exercise TTL expiry path."""
        key = f"ttl:{random.randint(0, 50)}"
        self.client.post(
            "/data",
            json={"key": key, "value": random_value(16), "ttl": 5},
            name="/data PUT (ttl)",
        )


# ---------------------------------------------------------------------------
# Write-heavy user — simulates an ingestion pipeline writing many unique keys
# ---------------------------------------------------------------------------
class WriteHeavyUser(HttpUser):
    weight = 20
    wait_time = between(0.01, 0.1)

    @task(3)
    def put_unique(self):
        """Write a unique key each time to stress the hash ring fan-out."""
        key = f"ingest:{random.randint(0, 10000)}"
        self.client.post(
            "/data",
            json={"key": key, "value": random_value(64)},
            name="/data PUT (unique)",
        )

    @task(1)
    def delete(self):
        key = random_key()
        with self.client.delete(
            f"/data/{key}",
            name="/data/[key] DELETE",
            catch_response=True,
        ) as resp:
            # 404 is fine — key may not exist
            if resp.status_code in (200, 404):
                resp.success()
            else:
                resp.failure(f"Unexpected status {resp.status_code}")

    @task(1)
    def get_after_write(self):
        """Write then immediately read the same key — tests replication consistency."""
        key = f"consistency:{random.randint(0, 100)}"
        value = random_value(32)
        put_resp = self.client.post(
            "/data",
            json={"key": key, "value": value},
            name="/data PUT (consistency)",
        )
        if put_resp.status_code == 200:
            with self.client.get(
                f"/data/{key}",
                name="/data GET (consistency)",
                catch_response=True,
            ) as get_resp:
                if get_resp.status_code == 200:
                    body = get_resp.json()
                    if body.get("value") != value:
                        get_resp.failure(
                            f"Consistency violation: wrote {value!r}, read {body.get('value')!r}"
                        )
                    else:
                        get_resp.success()
                elif get_resp.status_code == 404:
                    # Acceptable under high eviction pressure
                    get_resp.success()
                else:
                    get_resp.failure(f"Unexpected status {get_resp.status_code}")


# ---------------------------------------------------------------------------
# Bulk user — exercises the bulk endpoints
# ---------------------------------------------------------------------------
class BulkUser(HttpUser):
    weight = 10
    wait_time = between(0.5, 2.0)

    def _random_batch(self, size=20):
        return [
            {"key": f"bulk:{random.randint(0, 200)}", "value": random_value(24)}
            for _ in range(size)
        ]

    @task(2)
    def bulk_put(self):
        batch = self._random_batch(size=random.randint(10, 50))
        self.client.post(
            "/data/bulk",
            json=batch,
            name="/data/bulk PUT",
        )

    @task(1)
    def bulk_get(self):
        keys = [f"bulk:{random.randint(0, 200)}" for _ in range(random.randint(10, 30))]
        with self.client.post(
            "/data/bulk/get",
            json=keys,
            name="/data/bulk/get POST",
            catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Unexpected status {resp.status_code}")

    @task(1)
    def health(self):
        self.client.get("/health", name="/health")
