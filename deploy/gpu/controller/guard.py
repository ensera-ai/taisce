# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""The operator-local lifecycle guard for a disposable GPU.ai node.

Owns exactly one instance for the life of this process: registers an ephemeral SSH key, picks the
cheapest eligible offering under the price cap, creates the instance under a unique run name with a
provider-side runtime limit, waits for it to answer, then on any exit (success, failure, interrupt)
wipes the node, terminates it, verifies it is gone and removes the key. The provider key is read
from GPUAI_API_KEY and never reaches the node. A pool that errored during boot this run is tried
again only when no other pool is eligible.

    GPUAI_API_KEY=… python3 guard.py --workdir run --gpus 8 --max-price 10 --hours 8
"""
import argparse, json, os, shlex, subprocess, sys, time, urllib.error, urllib.request, uuid
from pathlib import Path
from urllib.parse import quote

API = 'https://api.gpu.ai/v1'


def parse(argv):
    p = argparse.ArgumentParser(description=__doc__.split('\n\n')[0])
    p.add_argument('--workdir', required=True, help='where the run state, the key and the results go; created 0700')
    p.add_argument('--gpus', type=int, default=8)
    p.add_argument('--gpu-type', default='a100_80gb')
    p.add_argument('--max-price', type=float, default=10.0, help='dollars per hour, the cap the provider enforces')
    p.add_argument('--hours', type=int, default=8, help='the provider-side auto-termination guard')
    p.add_argument('--min-disk-gb', type=int, default=1000)
    p.add_argument('--name-prefix', default='taisce')
    p.add_argument('--check', action='store_true', help='validate arguments and the key, then exit')
    a = p.parse_args(argv)
    if a.gpus < 1 or a.max_price <= 0 or a.hours < 1 or a.hours > 48:
        p.error('gpus must be positive, max-price positive, hours 1..48')
    return a


def key_from_env():
    key = os.environ.get('GPUAI_API_KEY', '').strip()
    if not key:
        raise SystemExit('GPUAI_API_KEY is not set; the provider key is read from the environment only')
    return key


class Provider:
    def __init__(self, key):
        self.key = key

    def call(self, method, path, body=None, idem=None):
        headers = {'Authorization': 'Bearer ' + self.key, 'Content-Type': 'application/json'}
        if idem:
            headers['Idempotency-Key'] = idem
        for attempt in range(4):
            try:
                req = urllib.request.Request(API + path, method=method, headers=headers, data=None if body is None else json.dumps(body).encode())
                with urllib.request.urlopen(req, timeout=30) as r:
                    return json.loads(r.read() or b'{}')
            except urllib.error.HTTPError as e:
                if e.code == 404:
                    return {'not_found': True}
                if e.code not in (429, 500, 502, 503, 504):
                    problem = json.loads(e.read() or b'{}')
                    raise RuntimeError(f"provider HTTP {e.code}: {problem.get('code', '')} {problem.get('detail', '')}") from None
            except (TimeoutError, urllib.error.URLError):
                pass
            time.sleep(2 ** attempt)
        raise RuntimeError('provider request retries exhausted')

    def instances(self):
        rows, cursor = [], None
        while True:
            page = self.call('GET', '/instances' + ('?cursor=' + quote(cursor, safe='') if cursor else ''))
            rows.extend(page.get('data', []))
            cursor = page.get('next_cursor')
            if not cursor:
                return rows


def log(message):
    print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), message, flush=True)


def main(argv):
    a = parse(argv)
    key = key_from_env()
    provider = Provider(key)
    root = Path(a.workdir).resolve()
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(root, 0o700)

    def save(name, value):
        p = root / name
        p.write_text(json.dumps(value, indent=2))
        p.chmod(0o600)

    if a.check:
        offers = provider.call('GET', f'/pricing?gpu_type={a.gpu_type}')['data']
        eligible = [o for o in offers if o['gpu_count'] == a.gpus and o.get('available', 0) > 0 and o['price_per_hour'] <= a.max_price and o.get('deployment_type') == 'vm']
        log(f'{len(eligible)} eligible offering(s) for {a.gpus} x {a.gpu_type} at or under ${a.max_price}/hour')
        return 0
    run = a.name_prefix + '-' + uuid.uuid4().hex[:10]
    save('run.json', {'name': run, 'max_price_per_hour': a.max_price, 'runtime_hours': a.hours, 'gpus': a.gpus, 'gpu_type': a.gpu_type})
    subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(root / 'ssh_key')], check=True)
    owned, key_id, connection, offer = None, None, None, None
    try:
        key_id = provider.call('POST', '/ssh-keys', {'name': run, 'public_key': (root / 'ssh_key.pub').read_text().strip()}, run + '-key')['id']
        save('key-registration.json', {'id': key_id})
        offers = provider.call('GET', f'/pricing?gpu_type={a.gpu_type}')['data']
        eligible = [o for o in offers if o['gpu_type'] == a.gpu_type and o['gpu_count'] == a.gpus and o.get('available', 0) > 0
                    and o['price_per_hour'] <= a.max_price and o.get('deployment_type') == 'vm' and o.get('instance_disk_gb', 0) >= a.min_disk_gb]
        if not eligible:
            raise RuntimeError(f'no eligible {a.gpus} x {a.gpu_type} VM at or under the price cap')
        failed_file = root.parent / 'failed-regions.txt'
        failed = set(failed_file.read_text().split()) if failed_file.exists() else set()
        untried = [o for o in eligible if o['region'] not in failed]
        offer = sorted(untried or eligible, key=lambda o: (o['price_per_hour'], o['region']))[0]
        save('selected-offer.json', offer)
        log(f"Creating {a.gpus} x {a.gpu_type} VM in {offer['region']} at ${offer['price_per_hour']}/hour (cap ${a.max_price})")
        op = provider.call('POST', '/instances', {'gpu_type': a.gpu_type, 'gpu_count': a.gpus, 'tier': 'on_demand', 'offering_id': offer['offering_id'],
                                                  'name': run, 'ssh_key_ids': [key_id], 'max_price_per_hour': a.max_price,
                                                  'viewed_price_per_hour': offer['price_per_hour'], 'auto_terminate_hours': a.hours, 'environment': 'raw_vm'}, run + '-create')
        save('create-operation.json', op)
        deadline = time.monotonic() + 1200
        previous = None
        while time.monotonic() < deadline:
            op = provider.call('GET', '/operations/' + op['operation_id'])
            if op.get('resource_id'):
                owned = op['resource_id']
                save('owned.json', {'id': owned})
            if op['state'] in ('failed', 'cancelled'):
                save('provision-failure.json', op)
                raise RuntimeError('provision operation ' + op['state'])
            if owned:
                inst = provider.call('GET', '/instances/' + owned)
                if inst.get('status') != previous:
                    previous = inst.get('status')
                    log('Instance status: ' + str(previous))
                if previous == 'running':
                    if inst.get('gpu_count') != a.gpus or inst.get('price_per_hour', 1e9) > a.max_price:
                        raise RuntimeError('provisioned hardware/price differs from request')
                    connection = inst.get('connection')
                    if connection and connection.get('hostname') and connection.get('port'):
                        save('ready.json', inst)
                        log('Instance ready; lifecycle guard is active')
                        break
                if previous in ('error', 'terminated'):
                    with open(failed_file, 'a') as f:
                        f.write(offer['region'] + '\n')
                    raise RuntimeError('instance ' + previous)
            time.sleep(10)
        else:
            raise RuntimeError('provision deadline exceeded')
        deadline = time.monotonic() + a.hours * 3600 - 300
        while time.monotonic() < deadline and not (root / 'finish').exists():
            time.sleep(5)
    except BaseException as error:
        log('Run controller failure: ' + str(error))
        save('controller-failure.json', {'error': str(error)})
    finally:
        for attempt in range(6):
            try:
                ids = {i['id'] for i in provider.instances() if i.get('name') == run}
                if owned:
                    ids.add(owned)
                for instance_id in ids:
                    inst = provider.call('GET', '/instances/' + instance_id)
                    conn = inst.get('connection') or connection
                    if conn and conn.get('hostname') and conn.get('port'):
                        ssh = ['ssh', '-i', str(root / 'ssh_key'), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15',
                               '-o', 'StrictHostKeyChecking=accept-new', '-o', 'UserKnownHostsFile=' + str(root / 'known_hosts'), '-p', str(conn['port']), 'root@' + conn['hostname']]
                        try:
                            result = subprocess.run(ssh + ['bash /root/taisce-qualification/wipe.sh'], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=300)
                            save('wipe-status.json', {'returncode': result.returncode})
                            log('Remote wipe exit: ' + str(result.returncode))
                        except Exception as error:
                            log('Remote wipe unavailable: ' + type(error).__name__)
                    provider.call('DELETE', '/instances/' + instance_id, idem=run + '-delete-' + instance_id)
                    log('Termination requested for owned instance')
                end = time.monotonic() + 600
                while time.monotonic() < end:
                    active = {i['id'] for i in provider.instances()}
                    terminal = all(provider.call('GET', '/instances/' + i).get('status') == 'terminated' or provider.call('GET', '/instances/' + i).get('not_found') for i in ids)
                    if not ids.intersection(active) and terminal:
                        save('termination.json', {'verified': True, 'ids': sorted(ids), 'time': time.time()})
                        log('Verified: owned instances terminated and absent from active inventory')
                        if key_id:
                            provider.call('DELETE', '/ssh-keys/' + key_id)
                        for p in (root / 'ssh_key', root / 'ssh_key.pub'):
                            p.unlink(missing_ok=True)
                        break
                    time.sleep(10)
                else:
                    raise RuntimeError('termination verification deadline exceeded')
                break
            except Exception as error:
                log('Cleanup retry: ' + str(error))
                time.sleep(10)
        else:
            save('CLEANUP-REQUIRES-ATTENTION.json', {'name': run, 'id': owned})
            log('CLEANUP REQUIRES ATTENTION; provider runtime limit remains configured')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
