# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""Bounded structured-output throughput check for the disposable GPU node."""

import concurrent.futures
import json
import math
import ssl
import statistics
import time
import urllib.request

ENDPOINT = "https://generation:8000/v1/chat/completions"
MODEL = "Qwen/Qwen3.8-27B"
REQUESTS = 140
CONCURRENCY = 28
SCHEMA = {
    "type": "object",
    "properties": {"claims": {"type": "array", "items": {"type": "string"}}},
    "required": ["claims"],
    "additionalProperties": False,
}
BODY = {
    "model": MODEL,
    "messages": [
        {
            "role": "user",
            "content": "Return one short claim stating that Dublin is in Ireland.",
        }
    ],
    "temperature": 0,
    "max_tokens": 80,
    "response_format": {
        "type": "json_schema",
        "json_schema": {"name": "claims", "strict": True, "schema": SCHEMA},
    },
}
CONTEXT = ssl.create_default_context(cafile="/tls/ca.crt")


def request_once(_index):
    started = time.perf_counter()
    request = urllib.request.Request(
        ENDPOINT,
        data=json.dumps(BODY).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, context=CONTEXT, timeout=180) as response:
        payload = json.load(response)
    value = json.loads(payload["choices"][0]["message"]["content"])
    assert isinstance(value.get("claims"), list) and value["claims"], value
    usage = payload.get("usage", {})
    return {
        "latency_seconds": time.perf_counter() - started,
        "prompt_tokens": int(usage.get("prompt_tokens", 0)),
        "completion_tokens": int(usage.get("completion_tokens", 0)),
    }


def percentile(values, percent):
    ordered = sorted(values)
    position = max(0, math.ceil(percent * len(ordered)) - 1)
    return ordered[position]


with concurrent.futures.ThreadPoolExecutor(max_workers=7) as executor:
    list(executor.map(request_once, range(7)))

started = time.perf_counter()
with concurrent.futures.ThreadPoolExecutor(max_workers=CONCURRENCY) as executor:
    results = list(executor.map(request_once, range(REQUESTS)))
elapsed = time.perf_counter() - started
latencies = [result["latency_seconds"] for result in results]
completion_tokens = sum(result["completion_tokens"] for result in results)
prompt_tokens = sum(result["prompt_tokens"] for result in results)
print(
    json.dumps(
        {
            "model": MODEL,
            "requests": REQUESTS,
            "concurrency": CONCURRENCY,
            "elapsed_seconds": round(elapsed, 6),
            "requests_per_second": round(REQUESTS / elapsed, 6),
            "prompt_tokens": prompt_tokens,
            "prompt_tokens_per_second": round(prompt_tokens / elapsed, 6),
            "completion_tokens": completion_tokens,
            "completion_tokens_per_second": round(completion_tokens / elapsed, 6),
            "latency_seconds": {
                "mean": round(statistics.fmean(latencies), 6),
                "p50": round(percentile(latencies, 0.50), 6),
                "p95": round(percentile(latencies, 0.95), 6),
                "p99": round(percentile(latencies, 0.99), 6),
                "max": round(max(latencies), 6),
            },
            "structured_outputs_valid": REQUESTS,
        },
        sort_keys=True,
    )
)
