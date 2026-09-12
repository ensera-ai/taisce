# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""One disposable GPU.ai node that serves the extraction model to this machine, for a demo.

    GPUAI_API_KEY=… python3 demo.py up --workdir ~/.taisce-demo --model 3.8 --check
    GPUAI_API_KEY=… python3 demo.py up --workdir ~/.taisce-demo --model 3.8
    GPUAI_API_KEY=… python3 demo.py status --workdir ~/.taisce-demo
    GPUAI_API_KEY=… python3 demo.py down --workdir ~/.taisce-demo

`up` owns the node for as long as it runs, the way guard.py owns a qualification node. It launches the
provider's base container in the named region family, installs a pinned vLLM on it over SSH, starts
the qualified serving arguments for the chosen model bound to the container's loopback, waits until
the model answers, then holds an SSH tunnel from 127.0.0.1:<port> on this machine to it. On any exit —
`down`, an interrupt, the runtime deadline, a failure — it closes the tunnel, terminates the instance,
verifies it is gone from the active inventory and removes the SSH key it registered. The provider-side
auto-termination is the fallback if this machine disappears first.

Why a base container and an install rather than the qualified image. Container capacity refuses an
image that defines its own ENTRYPOINT, and the check reads the image rather than the launch request:
clearing the ENTRYPOINT in the request was refused the same way (both measured 2026-09-11). Launching
without pinning an offering, the provider's other suggestion, gives up the placement and price the
launch was checked against. So the server is the qualified arguments on a pinned vLLM release rather
than the qualified image, and docs/36-extraction-models.md records the corpus on this path.

What it protects, and from whom. The asset that leaves this machine is conversation text sent for
extraction, and the node's GPU time is money. No port is exposed on the node, so vLLM is reachable
only through the SSH tunnel; it binds to the container's loopback and also requires a per-run bearer
key, so a tunnel later bound beyond this machine's loopback is still not an open endpoint. The key is
generated here and placed on the node over SSH, so it is never in the launch request or the provider's
metadata. `up` proves the key is enforced before it reports ready. The region prefix is a hard filter
rather than a preference: when nothing in it is eligible the launch is refused, never placed elsewhere.

What it does not cover: whoever operates the host. Secure capacity is a provider's data-centre
partner and community capacity a third party; either way the text being extracted is in that
machine's memory while it is processed. That is fine for a demo on made-up conversations and is no
place for real ones. Termination is a logical wipe of the container, not a claim about the provider's
storage.
"""
import argparse, json, os, re, secrets, shlex, signal, socket, subprocess, sys, time, urllib.error, urllib.request, uuid
from pathlib import Path

from guard import Provider, key_from_env, log

# The two models a demo can serve, as the first arguments to `vllm serve`. 3.8 is the model the
# qualification measured and the one this path passed the corpus with (docs/36). 3.6 is the family of
# the local default, at full precision rather than the local 8-bit build; it has not been served on this
# path yet, and the corpus is what says whether it holds there.
VARIANTS = {
    '3.8': ['Qwen/Qwen3.8-27B', '--revision=1d4bf0f2ff6012fd82039f2fa52739d0dd7c60c0'],
    '3.6': ['Qwen/Qwen3.6-35B-A3B', '--revision=995ad96eacd98c81ed38be0c5b274b04031597b0'],
}
# BF16 weights in GB: 27.8B and 36.0B parameters at two bytes each.
WEIGHTS_GB = {'3.8': 56, '3.6': 72}
# Card memory in GB for the types a demo may ask for. A type not listed is refused rather than guessed.
CARD_GB = {'a100_80gb': 80, 'h100_pcie': 80, 'h100_sxm': 80, 'h100_nvl': 94, 'rtx_pro_6000': 96,
           'h200_nvl': 141, 'h200_sxm': 141, 'b200': 192, 'b300': 288}
# What vLLM needs beyond the weights for its cache and CUDA graphs. Twelve is a floor chosen between what
# the measured 3.8 run left on a 96 GB card (about 35 GB) and what an 80 GB card would leave 3.6 (about
# 4 GB); it is not a measured minimum.
HEADROOM_GB = 12

# The rest of the arguments the qualification measured (compose.gpu.yaml, D116), so the demo serves the
# configuration that holds the extraction contract rather than a lookalike. Thinking is off at the
# server: extraction is a judgement over a closed vocabulary, a reasoning trace is paid for on every
# call, and with it on Qwen3.8 did not answer inside the client's bound (docs/36). The TLS files are left
# out because the far end of the tunnel is the container's loopback; the host and port are added for
# the same reason. TestTheDemoServesTheQualifiedArgumentsOnAPinnedRelease holds these, and the 3.8
# revision, equal to the compose file.
SERVE = [
    '--dtype=bfloat16',
    '--tensor-parallel-size=1',
    '--structured-outputs-config.backend=guidance',
    '--max-model-len=32768',
    '--max-num-seqs=8',
    '--gpu-memory-utilization=0.95',
    '--reasoning-parser=qwen3',
    '--default-chat-template-kwargs={"enable_thinking":false}',
    '--host=127.0.0.1',
    '--port=8000',
]

# Pinned, so two runs install the same server. uv resolves vLLM's exact torch pin in seconds where pip
# takes minutes; neither is on the base image.
VLLM = 'vllm==0.29.0'
UV = 'uv==0.12.13'

# The pinned release's torch is built for CUDA 13, so a driver that reports less cannot run it. It is
# read on the node before anything is installed, and refused rather than worked around.
MIN_DRIVER_CUDA = (13, 0)

# Runs on the node. Each step past the install is there because a launch on 2026-09-11 died without it:
#   - Python headers. Triton compiles a helper against Python.h when vLLM starts; the base image has
#     none, and the server died inspecting the model.
#   - VLLM_USE_FLASHINFER_SAMPLER=0. FlashInfer's sampler compiles on first use and found no target
#     architecture on a node without a CUDA compiler, so warm-up died on an RTX PRO 6000. Extraction
#     decodes greedily, so the sampler vLLM falls back to changes nothing a request can observe.
SETUP = '''set -euo pipefail
test -s /root/.vllm_key
export DEBIAN_FRONTEND=noninteractive PATH=/root/.local/bin:/usr/local/bin:$PATH
python3 -c 'import os, sys, sysconfig; sys.exit(not os.path.exists(os.path.join(sysconfig.get_paths()["include"], "Python.h")))' \\
  || {{ apt-get update -qq && apt-get install -y -qq python3-dev >/dev/null; }}
python3 -m pip --version >/dev/null 2>&1 || {{ apt-get update -qq && apt-get install -y -qq python3-pip >/dev/null; }}
command -v uv >/dev/null 2>&1 || python3 -m pip install -q '{uv}'
[ -x /opt/vllm/bin/python ] || uv venv -q /opt/vllm --python python3
uv pip install -q --python /opt/vllm/bin/python '{vllm}'
pkill -f '[v]llm serve' || true
cd /root
nohup env VLLM_API_KEY="$(cat /root/.vllm_key)" HF_HOME=/root/hf VLLM_USE_FLASHINFER_SAMPLER=0 \\
  /opt/vllm/bin/vllm serve {serve} > /root/vllm.log 2>&1 &
'''

# A server that died still has a log and sometimes a lingering process, so a failed start is read from
# the log as well as from the process table rather than waited out for an hour.
CRASHED = ("grep -qE 'Traceback|Engine core initialization failed|fatal error|CUDA error|out of memory' /root/vllm.log"
           " || ! pgrep -f '[v]llm serve' >/dev/null")
HEALTHY = "python3 -c \"import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/health', timeout=5)\""


def setup_script(variant):
    return SETUP.format(uv=UV, vllm=VLLM, serve=shlex.join(VARIANTS[variant] + SERVE))


def inside_git_worktree(path):
    # The workdir holds an SSH private key and a bearer key. Inside a worktree they are one `git add .`
    # from being committed, so a workdir there is refused; without git to ask, it is refused as well.
    probe = path
    while not probe.exists():
        probe = probe.parent
    try:
        r = subprocess.run(['git', '-C', str(probe), 'rev-parse', '--is-inside-work-tree'], capture_output=True, text=True)
    except FileNotFoundError:
        return True
    return r.returncode == 0 and r.stdout.strip() == 'true'


def parse(argv):
    p = argparse.ArgumentParser(description=__doc__.split('\n\n')[0])
    p.add_argument('action', choices=['up', 'status', 'down'])
    p.add_argument('--workdir', required=True, help='run state and both keys; created 0700, never inside a Git worktree')
    p.add_argument('--model', choices=sorted(VARIANTS), default='3.8', help='3.8 is the measured one; 3.6 needs a card of 94 GB or more')
    p.add_argument('--region-prefix', default='eu', help='only regions starting with this are eligible; never widened')
    p.add_argument('--gpu-type', default='rtx_pro_6000', help='the card this path was measured on')
    p.add_argument('--max-price', type=float, default=2.5, help='dollars per hour, disk included; the cap the provider enforces')
    p.add_argument('--hours', type=int, default=4, help='the provider-side auto-termination guard')
    p.add_argument('--disk-gb', type=int, default=100, help='the weights are 56 GB for 3.8 and 72 GB for 3.6')
    p.add_argument('--port', type=int, default=18000, help='the tunnel listens on 127.0.0.1:<port>')
    p.add_argument('--check', action='store_true', help='validate arguments and the key and list eligible offerings, then exit')
    a = p.parse_args(argv)
    if not re.fullmatch(r'[a-z][a-z-]*', a.region_prefix):
        p.error('region-prefix must name a region family, e.g. eu')
    if a.max_price <= 0 or not 1 <= a.hours <= 48 or not 10 <= a.disk_gb <= 1000 or not 1024 <= a.port <= 65535:
        p.error('max-price must be positive, hours 1..48, disk-gb 10..1000, port 1024..65535')
    if a.gpu_type not in CARD_GB:
        p.error(f'unknown gpu-type {a.gpu_type}; known: {", ".join(sorted(CARD_GB))}')
    if CARD_GB[a.gpu_type] * 0.95 - WEIGHTS_GB[a.model] < HEADROOM_GB:
        p.error(f'Qwen{a.model} has {WEIGHTS_GB[a.model]} GB of weights, which leaves a {a.gpu_type} too little memory for its cache')
    if a.disk_gb < WEIGHTS_GB[a.model] + 10:
        p.error(f'disk-gb must hold the {WEIGHTS_GB[a.model]} GB of weights and the server')
    a.workdir = Path(a.workdir).expanduser().resolve()
    if inside_git_worktree(a.workdir):
        p.error('the workdir holds keys and must be outside any Git worktree')
    return a


def price_of(offer, a):
    # Disk past the included allowance is folded into the hourly rate, so the cap is applied to what
    # the instance will actually cost rather than to the catalog row.
    return offer['price_per_hour'] + max(0, a.disk_gb - 100) * offer.get('disk_price_per_gb_hour', 0)


def eligible(offers, a):
    # Container offerings only: SSH then lands inside the container vLLM runs in, and its loopback is the
    # tunnel's far end. It is also the only kind this path has been run on.
    rows = [o for o in offers
            if o.get('gpu_type') == a.gpu_type and o.get('gpu_count') == 1 and o.get('available', 0) > 0
            and o.get('deployment_type') == 'container' and str(o.get('region', '')).startswith(a.region_prefix)
            and price_of(o, a) <= a.max_price
            and (a.disk_gb <= o.get('instance_disk_gb', 0) or (o.get('disk_configurable') and a.disk_gb <= o.get('storage_gb', 0)))]
    return sorted(rows, key=lambda o: (price_of(o, a), o['region']))


class State:
    def __init__(self, root):
        self.root = root

    def save(self, name, value):
        p = self.root / name
        p.write_text(value if isinstance(value, str) else json.dumps(value, indent=2))
        p.chmod(0o600)

    def load(self, name):
        p = self.root / name
        return json.loads(p.read_text()) if p.exists() else None


def ssh_parts(state, conn):
    host, port = conn.get('hostname', ''), int(conn.get('port', 0))
    if not re.fullmatch(r'[A-Za-z0-9.-]+', host) or not 1 <= port <= 65535:
        raise RuntimeError('the provider returned an unusable SSH endpoint')
    user = 'root'
    m = re.search(r'([A-Za-z0-9._-]+)@', conn.get('ssh_command') or '')
    if m:
        user = m.group(1)
    options = ['ssh', '-i', str(state.root / 'ssh_key'), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15',
               '-o', 'StrictHostKeyChecking=accept-new', '-o', 'UserKnownHostsFile=' + str(state.root / 'known_hosts'),
               '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=3', '-p', str(port)]
    return options, user + '@' + host


def remote(options, dest, command, timeout=60, stdin=None):
    try:
        return subprocess.run(options + [dest, command], input=stdin, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return subprocess.CompletedProcess(command, 124, '')


def http(url, key=None, body=None, timeout=120):
    headers = {'Content-Type': 'application/json'}
    if key:
        headers['Authorization'] = 'Bearer ' + key
    req = urllib.request.Request(url, headers=headers, data=None if body is None else json.dumps(body).encode())
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read() or b'{}')
    except urllib.error.HTTPError as e:
        return e.code, {}


def open_tunnel(options, dest, port):
    tunnel = subprocess.Popen(options + ['-N', '-o', 'ExitOnForwardFailure=yes', '-L', f'127.0.0.1:{port}:127.0.0.1:8000', dest],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    end = time.monotonic() + 30
    while time.monotonic() < end:
        if tunnel.poll() is not None:
            raise RuntimeError(f'the tunnel exited ({tunnel.returncode}); is 127.0.0.1:{port} already in use?')
        try:
            socket.create_connection(('127.0.0.1', port), timeout=2).close()
            return tunnel
        except OSError:
            time.sleep(1)
    tunnel.terminate()
    raise RuntimeError('the tunnel did not open')


def prepare_node(options, dest, api_key, variant):
    # SSH answers a little after the provider reports running.
    for attempt in range(30):
        if remote(options, dest, 'true', 30).returncode == 0:
            break
        time.sleep(10)
    else:
        raise RuntimeError('SSH did not answer')
    card = remote(options, dest, 'nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv,noheader', 60).stdout.strip()
    m = re.search(r'CUDA Version:\s*(\d+)\.(\d+)', remote(options, dest, 'nvidia-smi', 60).stdout)
    if not m:
        raise RuntimeError('nvidia-smi reported no CUDA version')
    driver_cuda = (int(m.group(1)), int(m.group(2)))
    log(f'Node: {card}; driver supports CUDA {driver_cuda[0]}.{driver_cuda[1]}')
    if driver_cuda < MIN_DRIVER_CUDA:
        raise RuntimeError(f'{VLLM} needs a driver for CUDA {MIN_DRIVER_CUDA[0]}.{MIN_DRIVER_CUDA[1]}; this node offers '
                           f'{driver_cuda[0]}.{driver_cuda[1]}')
    if remote(options, dest, 'umask 077; cat > /root/.vllm_key', 30, api_key + '\n').returncode != 0:
        raise RuntimeError('could not place the bearer key on the node')
    log(f'Installing {VLLM} and starting {VARIANTS[variant][0]}')
    r = remote(options, dest, 'bash -s', 1200, setup_script(variant))
    if r.returncode != 0:
        raise RuntimeError('the node setup failed: ' + r.stdout.strip()[-400:])


def wait_for_health(options, dest, variant):
    log(f'Waiting for the model: {WEIGHTS_GB[variant]} GB of weights load after the download')
    deadline, last_progress = time.monotonic() + 3600, 0
    time.sleep(15)
    while time.monotonic() < deadline:
        if remote(options, dest, HEALTHY, 30).returncode == 0:
            return
        if remote(options, dest, CRASHED, 30).returncode == 0:
            tail = remote(options, dest, "grep -E 'Error|error' /root/vllm.log | tail -3", 30).stdout.strip()
            raise RuntimeError('vLLM failed to start: ' + tail[-400:])
        if time.monotonic() - last_progress > 120:
            progress = remote(options, dest, "du -sh /root/hf 2>/dev/null | cut -f1; grep -v '^\\s*$' /root/vllm.log | tail -1", 30)
            log('  ' + ' | '.join(progress.stdout.split('\n'))[:200])
            last_progress = time.monotonic()
        time.sleep(20)
    raise RuntimeError('the model did not become healthy within an hour')


def prove_the_endpoint(port, key, model):
    # Ready is a claim about three properties, so each is checked through the tunnel rather than assumed
    # from the launch arguments: the key is enforced, the chosen model is what answers, and it answers
    # without a reasoning trace.
    base = f'http://127.0.0.1:{port}/v1'
    status, _ = http(base + '/models')
    if status != 401:
        raise RuntimeError(f'the model answered {status} without the key; refusing to hold an open endpoint')
    status, models = http(base + '/models', key)
    if status != 200 or model not in [m.get('id') for m in models.get('data', [])]:
        raise RuntimeError(f'{model} is not what the endpoint serves ({status})')
    status, reply = http(base + '/chat/completions', key, {
        'model': model, 'temperature': 0, 'max_tokens': 64, 'response_format': {'type': 'json_object'},
        'messages': [{'role': 'user', 'content': 'Reply with the JSON object {"ok": true} and nothing else.'}]})
    if status != 200:
        raise RuntimeError(f'a chat completion failed ({status})')
    message = reply['choices'][0]['message']
    if message.get('reasoning_content') or message.get('reasoning'):
        raise RuntimeError('the model produced a reasoning trace; thinking is not off')
    log('Endpoint proved: the key is enforced, ' + model + ' answers, and it answers without thinking')


def teardown(provider, state, run, owned, key_id):
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    for attempt in range(6):
        try:
            ids = {i['id'] for i in provider.instances() if i.get('name') == run}
            if owned:
                ids.add(owned)
            for instance_id in ids:
                provider.call('DELETE', '/instances/' + instance_id, idem=run + '-delete-' + instance_id)
                log('Termination requested for ' + instance_id)
            end = time.monotonic() + 600
            while time.monotonic() < end:
                active = {i['id'] for i in provider.instances()}
                rows = [provider.call('GET', '/instances/' + i) for i in ids]
                if not ids & active and all(r.get('not_found') or r.get('status') == 'terminated' for r in rows):
                    state.save('termination.json', {'verified': True, 'ids': sorted(ids), 'time': time.time()})
                    if key_id:
                        provider.call('DELETE', '/ssh-keys/' + key_id)
                    for name in ('ssh_key', 'ssh_key.pub', 'api_key', 'endpoint.json', 'pid'):
                        (state.root / name).unlink(missing_ok=True)
                    log('Verified: the node is terminated and absent from the active inventory; keys removed')
                    return True
                time.sleep(10)
            raise RuntimeError('termination verification deadline exceeded')
        except Exception as error:
            log('Cleanup retry: ' + str(error))
            time.sleep(10)
    state.save('CLEANUP-REQUIRES-ATTENTION.json', {'name': run, 'id': owned})
    log('CLEANUP REQUIRES ATTENTION; the provider-side auto-termination remains configured')
    return False


def up(a, provider, state):
    if state.load('owned.json') and not state.load('termination.json'):
        raise SystemExit(f'{state.root} already owns a node; run down first')
    for name in ('finish', 'termination.json', 'failure.json', 'endpoint.json', 'owned.json', 'CLEANUP-REQUIRES-ATTENTION.json'):
        (state.root / name).unlink(missing_ok=True)
    model = VARIANTS[a.model][0]
    run = 'taisce-demo-' + uuid.uuid4().hex[:10]
    state.save('run.json', {'name': run, 'model': model, 'region_prefix': a.region_prefix, 'max_price_per_hour': a.max_price,
                            'runtime_hours': a.hours})
    state.save('pid', str(os.getpid()))
    for name in ('ssh_key', 'ssh_key.pub'):
        (state.root / name).unlink(missing_ok=True)
    subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(state.root / 'ssh_key')], check=True)
    api_key = secrets.token_urlsafe(32)
    state.save('api_key', api_key)

    def stop(signum, frame):
        raise SystemExit(f'signal {signum}')
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGHUP, stop)

    owned, key_id, tunnel = None, None, None
    try:
        key_id = provider.call('POST', '/ssh-keys', {'name': run, 'public_key': (state.root / 'ssh_key.pub').read_text().strip()}, run + '-key')['id']
        state.save('key-registration.json', {'id': key_id})
        offers = eligible(provider.call('GET', f'/pricing?gpu_type={a.gpu_type}')['data'], a)
        if not offers:
            raise RuntimeError(f'no 1 x {a.gpu_type} container in {a.region_prefix}* at or under ${a.max_price}/hour; not placing it elsewhere')
        offer = offers[0]
        state.save('selected-offer.json', offer)
        price = price_of(offer, a)
        log(f"Creating 1 x {a.gpu_type} container in {offer['region']} for {model} at ${price:.2f}/hour (cap ${a.max_price}), "
            f"auto-terminating after {a.hours}h; capacity class {offer.get('capacity_class', 'unknown')}")
        # No image and no environment: the provider's default base container is what this path was run
        # on, and no key or other secret goes into the request.
        op = provider.call('POST', '/instances', {
            'gpu_type': a.gpu_type, 'gpu_count': 1, 'tier': 'on_demand', 'offering_id': offer['offering_id'], 'region': offer['region'],
            'name': run, 'ssh_key_ids': [key_id], 'max_price_per_hour': a.max_price, 'viewed_price_per_hour': price,
            'auto_terminate_hours': a.hours, 'disk_gb': a.disk_gb}, run + '-create')
        state.save('create-operation.json', op)

        conn, previous, deadline = None, None, time.monotonic() + 1800
        while time.monotonic() < deadline:
            op = provider.call('GET', '/operations/' + op['operation_id'])
            if op.get('resource_id') and not owned:
                owned = op['resource_id']
                state.save('owned.json', {'id': owned})
            if op['state'] in ('failed', 'cancelled'):
                state.save('provision-failure.json', op)
                detail = (op.get('error') or {}).get('detail', '')
                raise RuntimeError('provision operation ' + op['state'] + (': ' + detail if detail else ''))
            if owned:
                inst = provider.call('GET', '/instances/' + owned)
                if inst.get('status') != previous:
                    previous = inst.get('status')
                    log('Instance status: ' + str(previous))
                if previous in ('error', 'terminated'):
                    raise RuntimeError('instance ' + previous + ': ' + str(inst.get('status_reason') or ''))
                if previous == 'running' and (inst.get('connection') or {}).get('hostname'):
                    if not str(inst.get('region', '')).startswith(a.region_prefix) or inst.get('gpu_count') != 1 \
                            or inst.get('price_per_hour', 1e9) > a.max_price:
                        raise RuntimeError('the provisioned region, hardware or price differs from the request')
                    conn = inst['connection']
                    break
            time.sleep(10)
        else:
            raise RuntimeError('provision deadline exceeded')

        options, dest = ssh_parts(state, conn)
        prepare_node(options, dest, api_key, a.model)
        wait_for_health(options, dest, a.model)
        tunnel = open_tunnel(options, dest, a.port)
        prove_the_endpoint(a.port, api_key, model)
        endpoint = f'http://localhost:{a.port}/v1'
        state.save('endpoint.json', {'endpoint': endpoint, 'model': model, 'allowlist': f'localhost:{a.port}', 'instance': owned,
                                     'region': offer['region'], 'price_per_hour': price, 'capacity_class': offer.get('capacity_class')})
        log(f'Ready: {endpoint} serves {model} with thinking off, from {offer["region"]}')
        log(f'  export TAISCE_INFERENCE_API_KEY="$(cat {state.root / "api_key"})"')
        log(f'  make test-inference INFERENCE_PROFILE=demo-qwen{a.model}')
        log(f'Stop with: python3 {Path(__file__).name} down --workdir {state.root}')

        restarts, deadline = 0, time.monotonic() + a.hours * 3600 - 300
        while time.monotonic() < deadline and not (state.root / 'finish').exists():
            if tunnel.poll() is not None:
                restarts += 1
                if restarts > 10:
                    raise RuntimeError('the tunnel keeps dropping')
                log('Tunnel dropped; reopening')
                tunnel = open_tunnel(options, dest, a.port)
            time.sleep(5)
        log('Finishing: ' + ('down was requested' if (state.root / 'finish').exists() else 'the runtime deadline is near'))
    except BaseException as error:
        log('Demo node failure: ' + (str(error) or type(error).__name__))
        state.save('failure.json', {'error': str(error) or type(error).__name__})
    finally:
        if tunnel and tunnel.poll() is None:
            tunnel.terminate()
        teardown(provider, state, run, owned, key_id)
    return 0 if state.load('termination.json') and not state.load('failure.json') else 1


def alive(pid):
    try:
        os.kill(pid, 0)
        return True
    except (OSError, ValueError):
        return False


def down(provider, state):
    (state.root / 'finish').touch()
    pid = int((state.root / 'pid').read_text()) if (state.root / 'pid').exists() else 0
    if pid and alive(pid):
        log('Asked the running demo to finish; waiting for verified termination')
        end = time.monotonic() + 1500
        while time.monotonic() < end:
            if state.load('termination.json') or (state.root / 'CLEANUP-REQUIRES-ATTENTION.json').exists():
                break
            time.sleep(5)
    else:
        # Nothing is holding the node — the machine slept, the terminal closed. Terminate from the
        # recorded identity rather than leave it to the provider's deadline.
        run = (state.load('run.json') or {}).get('name')
        if not run:
            raise SystemExit(f'{state.root} holds no demo run')
        if not state.load('termination.json'):
            teardown(provider, state, run, (state.load('owned.json') or {}).get('id'), (state.load('key-registration.json') or {}).get('id'))
    verified = state.load('termination.json')
    log('Terminated and verified' if verified else 'NOT verified; see ' + str(state.root))
    return 0 if verified else 1


def status(provider, state):
    endpoint = state.load('endpoint.json')
    owned = (state.load('owned.json') or {}).get('id')
    if state.load('termination.json'):
        log('No node: the last one was terminated and verified')
        return 0
    if not owned:
        log('No node')
        return 0
    inst = provider.call('GET', '/instances/' + owned)
    log(f"Instance {owned}: {inst.get('status')} in {inst.get('region')} at ${inst.get('price_per_hour')}/hour, "
        f"auto-terminates at {inst.get('auto_terminate_at')}")
    if endpoint:
        log(f"Serving {endpoint['model']} at {endpoint['endpoint']}")
    return 0


def main(argv):
    a = parse(argv)
    provider = Provider(key_from_env())
    if a.check:
        offers = eligible(provider.call('GET', f'/pricing?gpu_type={a.gpu_type}')['data'], a)
        log(f'{len(offers)} eligible 1 x {a.gpu_type} container offering(s) in {a.region_prefix}* at or under ${a.max_price}/hour '
            f'for {VARIANTS[a.model][0]}')
        for o in offers:
            log(f"  {o['region']} ${price_of(o, a):.2f}/hour, {o.get('capacity_class', 'unknown')} capacity")
        return 0
    a.workdir.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(a.workdir, 0o700)
    state = State(a.workdir)
    return {'up': lambda: up(a, provider, state), 'down': lambda: down(provider, state), 'status': lambda: status(provider, state)}[a.action]()


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
