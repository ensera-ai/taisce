# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""Exercise deployed API + asynchronous worker with synthetic, erasable evidence."""
import json
import os
import re
import shlex
import subprocess
import time
import urllib.request

# The runner names the compose files, because a generated topology file may be among them.
compose = shlex.split(os.environ.get('TAISCE_COMPOSE',
    'docker compose -p taisce-qualification -f compose.yaml -f compose.gpu.yaml'))
logs = subprocess.check_output(compose + ['logs', '--no-color', 'bootstrap'], text=True)
match = re.search(r'tsk_[A-Za-z0-9_-]+', logs)
if match is None:
    raise SystemExit('bootstrap credential missing')
token = match.group(0)

def request(path, body=None):
    req = urllib.request.Request('http://127.0.0.1:8080' + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=35) as response:
        return json.load(response)

subject = 'gpu-qualification-synthetic-subject'
try:
    observed = request('/v1/observations', {'data_subject_id': subject, 'messages': [
        {'role': 'user', 'content': 'I work at Ensera and I live in Dublin.'}]})
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        freshness = request('/v1/freshness')
        if freshness.get('formed') is not None and freshness['formed'] >= observed['log_offset']:
            break
        time.sleep(2)
    else:
        raise RuntimeError('deployed worker did not form observation before deadline')
    recalled = request('/v1/recalls', {'question': 'Where does the user work?', 'data_subject_id': subject})
    if not recalled.get('facts'):
        raise RuntimeError('deployed recall returned no supported facts')
    print('PASS deployed observation -> real-model formation -> subject recall')
finally:
    erased = request('/v1/erasures', {'data_subject_id': subject, 'reason': 'disposable qualification cleanup'})
    if not erased.get('clean') or any(erased.get('residual', {}).values()):
        raise RuntimeError('deployed erasure left residual data')
    print('PASS deployed erasure with zero residual')
recalled = request('/v1/recalls', {'question': 'Where does the user work?', 'data_subject_id': subject})
if recalled.get('facts'):
    raise RuntimeError('erased facts returned by deployed recall')
print('PASS erased subject recall is empty')
