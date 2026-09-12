# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""Deploy to the guard's instance, run the phases, collect every results directory.

Runs beside guard.py on the same workdir. It waits for ready.json, uploads `git archive` of the
named commit, the corpus directory when given, and the .NET adapter repository when given; arms a
remote wipe watchdog ahead of the provider deadline; runs the phases script detached on the node;
polls until the phases write `done`; pulls every results directory back; and touches `finish` so
the guard tears the node down. No credential and no source outside the named commits leaves this
machine.

    python3 deploy.py --workdir run --repo . --commit <sha> [--corpus DIR] [--dotnet-repo DIR --dotnet-commit <sha>] [--phases phases.sh] --hours 8
"""
import argparse, json, re, subprocess, sys, tarfile, time
from pathlib import Path


def parse(argv):
    p = argparse.ArgumentParser(description=__doc__.split('\n\n')[0])
    p.add_argument('--workdir', required=True)
    p.add_argument('--repo', required=True, help='the service repository the snapshot is archived from')
    p.add_argument('--commit', required=True)
    p.add_argument('--corpus', help='a licensed corpus directory to place at /root/corpus; never under results')
    p.add_argument('--dotnet-repo', help='the .NET adapter repository, for the head-to-head phase')
    p.add_argument('--dotnet-commit')
    p.add_argument('--phases', default=str(Path(__file__).resolve().parent / 'phases.sh'))
    p.add_argument('--fetch-corpus', action='store_true',
                   help='let the node download the pinned AP News benchmark corpus, whose licence '
                        'the operator accepts by passing this')
    p.add_argument('--hours', type=int, default=8, help='the guard\'s runtime, for the watchdog and the polling budget')
    p.add_argument('--min-gpus', type=int, default=8)
    p.add_argument('--check', action='store_true', help='validate arguments and inputs, then exit')
    a = p.parse_args(argv)
    for name, value in (('commit', a.commit), ('dotnet-commit', a.dotnet_commit)):
        if value is not None and not re.fullmatch(r'[0-9a-f]{7,40}', value):
            p.error(f'--{name} must be a commit hash')
    if bool(a.dotnet_repo) != bool(a.dotnet_commit):
        p.error('--dotnet-repo and --dotnet-commit go together')
    if a.corpus and not Path(a.corpus).is_dir():
        p.error('--corpus must be a directory')
    if not Path(a.phases).is_file():
        p.error('--phases must be a file')
    if a.hours < 1 or a.hours > 48:
        p.error('--hours is 1..48')
    for repo, commit in ((a.repo, a.commit), (a.dotnet_repo, a.dotnet_commit)):
        if repo and subprocess.run(['git', '-C', repo, 'cat-file', '-e', commit + '^{commit}'], capture_output=True).returncode != 0:
            p.error(f'{commit} is not a commit in {repo}')
    return a


def log(message):
    print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), message, flush=True)


class _Timed:
    returncode, stdout = 124, ''


def main(argv):
    a = parse(argv)
    if a.check:
        log('arguments and inputs are usable')
        return 0
    root = Path(a.workdir).resolve()
    runtime = a.hours * 3600
    try:
        while not (root / 'ready.json').exists():
            if (root / 'termination.json').exists() or (root / 'controller-failure.json').exists():
                raise SystemExit('instance did not become usable')
            time.sleep(5)
        c = json.loads((root / 'ready.json').read_text())['connection']
        assert re.fullmatch(r'[A-Za-z0-9.-]+', c['hostname']) and 1 <= int(c['port']) <= 65535
        ssh = ['ssh', '-i', str(root / 'ssh_key'), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15',
               '-o', 'StrictHostKeyChecking=accept-new', '-o', 'UserKnownHostsFile=' + str(root / 'known_hosts'),
               '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=3', '-p', str(c['port']), 'root@' + c['hostname']]
        (root / 'ssh-args.json').write_text(json.dumps(ssh))

        def remote(script, timeout=120):
            try:
                return subprocess.run(ssh + ['bash -s'], input=script, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
            except subprocess.TimeoutExpired:
                log(f'remote call timed out after {timeout}s')
                return _Timed()

        for attempt in range(12):
            r = remote('id; nvidia-smi -L; docker version --format {{.Server.Version}}; docker compose version; df -h /; free -h\n', 80)
            if r.returncode == 0:
                (root / 'host-inspection.log').write_text(r.stdout)
                break
            time.sleep(10)
        else:
            raise SystemExit('SSH inspection failed')
        seen = r.stdout.count('NVIDIA')
        log(f'node exposes {seen} NVIDIA card(s)')
        if seen < a.min_gpus:
            raise SystemExit(f'node exposes {seen} cards, fewer than the {a.min_gpus} asked for; the guard tears it down')

        def upload(archive, target):
            r = subprocess.run(ssh + [f'rm -rf {target} && mkdir -p {target} && tar -x -C {target}'], input=archive, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=600)
            assert r.returncode == 0, r.stdout.decode(errors='replace')[-500:]

        log('Uploading source snapshot ' + a.commit)
        upload(subprocess.run(['git', '-C', a.repo, 'archive', '--format=tar', a.commit], stdout=subprocess.PIPE, check=True).stdout, '/root/taisce-qualification')
        if a.dotnet_repo:
            log('Uploading .NET adapter snapshot ' + a.dotnet_commit)
            upload(subprocess.run(['git', '-C', a.dotnet_repo, 'archive', '--format=tar', a.dotnet_commit], stdout=subprocess.PIPE, check=True).stdout, '/root/taisce-dotnet')
        if a.corpus:
            log('Uploading corpus ' + a.corpus)
            with tarfile.open(root / 'corpus.tar', 'w') as tar:
                tar.add(a.corpus, arcname='corpus')
            with open(root / 'corpus.tar', 'rb') as f:
                r = subprocess.run(ssh + ['rm -rf /root/corpus && tar -x -C /root'], stdin=f, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=600)
            (root / 'corpus.tar').unlink()
            assert r.returncode == 0, r.stdout.decode(errors='replace')[-500:]
            log('Corpus on node: ' + remote("find /root/corpus -type f | wc -l; du -sh /root/corpus\n").stdout.strip().replace('\n', ' | '))

        watchdog = runtime - 900
        remote(f"setsid nohup bash -c 'sleep {watchdog}; bash /root/taisce-qualification/deploy/gpu/wipe.sh' < /dev/null > /root/watchdog.log 2>&1 &\n")
        (root / 'remote-watchdog.json').write_text(json.dumps({'scheduled_delay_seconds': watchdog}))
        phases = Path(a.phases).read_text()
        # --fetch-corpus is the operator saying the node may download the benchmark corpus, which
        # ships under a licence they accept rather than one this repository vendors. Passed as an
        # environment variable on the phases command so it is visible in the node's process list and
        # in this log, never baked into the script.
        fetch = 'TAISCE_FETCH_CORPUS=1 ' if a.fetch_corpus else ''
        remote("cat > /root/phases.sh <<'EOS'\n" + phases +
               "EOS\nsetsid nohup env " + fetch +
               "bash /root/phases.sh < /dev/null > /root/phases.log 2>&1 &\n", 60)
        r = remote("sleep 3; pgrep -f 'phases.sh' | head -1\n", 60)
        if not r.stdout.strip():
            raise SystemExit('the phases did not start on the node')
        log('phases pid on node: ' + r.stdout.strip())
        started = time.monotonic()
        last = ''
        while time.monotonic() - started < runtime - 1500:
            r = remote("cat /root/phases.txt 2>/dev/null; tail -n 2 /root/phases.log 2>/dev/null; tail -n 1 /root/taisce-qualification/results/test-runner.log 2>/dev/null; tail -n 1 /root/taisce-qualification/results/vocabulary-coverage.log 2>/dev/null\n", 60)
            text = r.stdout.strip()
            if text != last:
                log('node: ' + text.replace('\n', ' | ')[-500:])
                last = text
            if 'done' in [line.strip() for line in r.stdout.splitlines()]:
                log('phases finished')
                break
            time.sleep(60)
        else:
            log('the phases did not finish inside the budget; collecting what exists')
        log('Collecting results')
        dest = root / 'results'
        dest.mkdir(exist_ok=True, mode=0o700)
        r = subprocess.run(ssh + ["cd /root/taisce-qualification && tar -c results results-* /root/phases.txt /root/phases.log 2>/dev/null"], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=600)
        (root / 'results.tar').write_bytes(r.stdout)
        with tarfile.open(root / 'results.tar') as tar:
            for member in tar.getmembers():
                if not member.isfile() or '..' in member.name or member.size > 200 * 1024 * 1024:
                    continue
                member.name = member.name.lstrip('/').replace('root/', '')
                tar.extract(member, dest)
        log('Results collected into ' + str(dest))
    finally:
        (root / 'finish').write_text('done\n')
        log('Signalled the lifecycle guard to tear the node down')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
