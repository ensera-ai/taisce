# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""Provision, deploy and measure; on a bad node, tear down and try again, a bounded number of times.

Every argument after `--` goes to deploy.py; the guard's arguments come first.

    GPUAI_API_KEY=… python3 attempts.py --attempts 4 --gpus 8 --max-price 10 --hours 8 -- --repo . --commit <sha> --corpus DIR
"""
import argparse, subprocess, sys, time
from pathlib import Path

HERE = Path(__file__).resolve().parent


def main(argv):
    if '--' in argv:
        split = argv.index('--')
        guard_args, deploy_args = argv[:split], argv[split + 1:]
    else:
        guard_args, deploy_args = argv, []
    p = argparse.ArgumentParser(description=__doc__.split('\n\n')[0])
    p.add_argument('--attempts', type=int, default=4)
    p.add_argument('--workdir', default=str(Path.cwd() / 'gpu-run'))
    a, rest = p.parse_known_args(guard_args)
    if a.attempts < 1 or a.attempts > 12:
        p.error('--attempts is 1..12')
    base = Path(a.workdir).resolve()
    base.mkdir(parents=True, exist_ok=True, mode=0o700)
    for attempt in range(1, a.attempts + 1):
        stamp = time.strftime('%H%M%S', time.gmtime())
        run = base / f'run-{stamp}'
        print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), f'attempt {attempt} in {run}', flush=True)
        with open(base / f'guard-{stamp}.log', 'w') as g, open(base / f'deploy-{stamp}.log', 'w') as d:
            guard = subprocess.Popen([sys.executable, str(HERE / 'guard.py'), '--workdir', str(run)] + rest, stdout=g, stderr=subprocess.STDOUT)
            time.sleep(3)
            deploy = subprocess.Popen([sys.executable, str(HERE / 'deploy.py'), '--workdir', str(run)] + deploy_args, stdout=d, stderr=subprocess.STDOUT)
            guard.wait()
            deploy.wait()
        if 'Results collected' in (base / f'deploy-{stamp}.log').read_text():
            print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), f'attempt {attempt} collected results', flush=True)
            return 0
        print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), f'attempt {attempt} did not collect results', flush=True)
        time.sleep(60)
    return 1


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
