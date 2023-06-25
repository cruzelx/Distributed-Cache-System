from locust import HttpUser, TaskSet, task, between
import random
import json


class CacheBehaviour(TaskSet):
    @task
    def put(self):
        headers = {"Content-Type": "application/json"}
        payloads = [{"key": "water", "value": "Nile"},
                    {"key": "Interest", "value": "Dance"},
                    {"key": "Escape", "value": "World"}]

        self.client.post("/data", json.dumps(random.choice(payloads)), headers)

    @task
    def get(self):
        self.client.get(
            "/data/"+random.choice(["water", "Interest", "Escape"]))


class CaccheLoadTest(HttpUser):
    tasks = [CacheBehaviour]
    wait_time = between(1, 2)
    host = "http://localhost:8080"
