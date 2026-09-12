# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""Measure the read path at sizes this repository has never been able to reach.

Five open issues name a number and none has one, because each needs a graph large enough for the read
path's shape to be visible: hub skew, the scaling claim, amplification limits, anchor
plans at size and mixed-workload capacity. The corpus pass produced 13,711 facts with a
median degree of 1 and a maximum of 58 — under the fanout cap of 64 — so the cap never binds and the
latency half of #7 cannot be answered from it at all.

WHAT THESE NUMBERS ARE, AND ARE NOT. The graph here is SYNTHETIC. It is written through the operator
connection with a controlled degree distribution, so it measures how the read path behaves against a
graph of a given size and shape, and nothing whatever about extraction quality, answer quality or what
a real corpus looks like. Every document these numbers reach says so in the sentence that introduces
them. The corpus measurement (`vocabulary-coverage.py`) is the one that speaks for extraction; this
one speaks for the read path and stops there.

WHY IT WRITES FACTS DIRECTLY AND OBSERVES NOTHING. A million facts through formation is a million
model calls, which measures the provider. The read path does not know how a fact arrived: it reads
`fact`, `entity` and their indexes. So the loader writes those rows and the registrations erasure
needs, and the driver then goes through the public recall route like any caller — the half that is
being measured is the half that is real.

WHY EVERY REQUEST IS COUNTED BY OUTCOME. A throughput number built from requests that failed is a
throughput number for failing. Refusals are counted by status and by code, and a run that refused
anything says so beside its percentiles rather than under them.

NO LATENCY BUDGET IS CLAIMED. There is no agreed target for this product (docs/00), so these are
measurements and not a verdict against one. Saying which is part of rule 9 rather than an apology for
it.
"""
import json
import os
import random
import re
import shlex
import statistics
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

COMPOSE = shlex.split(os.environ.get('TAISCE_COMPOSE', 'docker compose'))
API = os.environ.get('TAISCE_API', 'http://127.0.0.1:8080')
SCHEMA = os.environ.get('TAISCE_SCHEMA', 'memory')
RESULTS = Path(os.environ.get('RESULTS', 'results'))
PROJECT = os.environ.get('SCALE_PROJECT', 'readpath')
# The sizes the scaling claim names, smallest first so a run that is cut short still leaves a
# curve rather than one point.
SIZES = [int(n) for n in os.environ.get('SCALE_SIZES', '50000,250000,1000000').split(',') if n]
CONCURRENCIES = [int(n) for n in os.environ.get('SCALE_CONCURRENCIES', '1,8,32,64').split(',') if n]
REQUESTS = int(os.environ.get('SCALE_REQUESTS', '400'))
# The traversal's own cap (domain.Fanout). Read from the environment so a build that changes it
# measures itself rather than this file's memory of it.
FANOUT = int(os.environ.get('SCALE_FANOUT', '64'))
SEED = int(os.environ.get('SCALE_SEED', '20260911'))

if not re.fullmatch(r'[a-z][a-z0-9_]{0,30}', PROJECT):
    raise SystemExit('SCALE_PROJECT must be a lowercase identifier')
RESULTS.mkdir(parents=True, exist_ok=True)


def log(message):
    print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()) + ' ' + message, flush=True)


def psql(sql, *, quiet=False):
    """Run one statement on the operator connection and return stdout.

    Through compose rather than a driver because this script has no Go build and no dependencies
    beyond the standard library, which is what lets it run on a node that only has the repository.
    """
    command = COMPOSE + ['exec', '-T', 'postgres', 'psql', '-v', 'ON_ERROR_STOP=1',
                         '-U', 'postgres', '-d', 'taisce', '-tAc', sql]
    done = subprocess.run(command, capture_output=True, text=True)
    if done.returncode != 0:
        if quiet:
            return ''
        raise SystemExit('psql failed: ' + done.stderr.strip()[-2000:])
    return done.stdout.strip()


# ── The graph ──────────────────────────────────────────────────────────────────────────────────
#
# One entity per thousand facts, plus a handful of hubs. The hubs are the point: #7 cannot be answered
# by a graph whose busiest node is under the cap, because the cap never binds and the cost of binding
# it is exactly what is unknown. So the distribution is built to straddle it — some nodes well under,
# some deliberately over — and the driver anchors on both.

def load(size):
    """Write `size` facts with a degree distribution that straddles the fanout cap."""
    log(f'loading {size} facts')
    started = time.time()
    # generate_series in the database rather than a million round trips. The shape is arithmetic on
    # the series index, so it is reproducible from this file without shipping a fixture.
    psql(f"""
WITH RECURSIVE nothing AS (SELECT 1)
INSERT INTO {SCHEMA}.entity (entity_id, scope, canonical_name, normalized_name, identity_kind, entity_type)
SELECT md5('scale-entity-' || n)::uuid, '{PROJECT}', 'Entity ' || n, 'entity ' || n, 'named', 'organisation'
  FROM generate_series(0, {max(size // 1000, FANOUT * 4)}) n
ON CONFLICT DO NOTHING""")
    # Every fact points at a subject drawn so that low indexes are busy: index 0 collects far more
    # than the cap, index 1 a little over it, and the long tail sits well under. A uniform draw would
    # produce a graph with no hub at all, which is the graph the corpus already gave us.
    psql(f"""
INSERT INTO {SCHEMA}.fact
  (fact_id, scope, subject_entity_id, object_entity_id, predicate, statement, cardinality, valid, source_role, data_subject_id)
SELECT md5('scale-fact-' || n)::uuid, '{PROJECT}',
       md5('scale-entity-' || (CASE
            WHEN n % 100 = 0 THEN 0
            WHEN n % 100 = 1 THEN 1
            ELSE 2 + (n % {max(size // 1000, FANOUT * 4)}) END))::uuid,
       md5('scale-entity-' || (2 + ((n * 7 + 3) % {max(size // 1000, FANOUT * 4)})))::uuid,
       'works_at', 'Entity ' || n || ' works at Entity ' || ((n * 7 + 3) % 1000),
       'many', '[2020-01-01,)'::tstzrange, 'user', 'scale-subject'
  FROM generate_series(1, {size}) n
ON CONFLICT DO NOTHING""")
    psql(f'ANALYZE {SCHEMA}.fact; ANALYZE {SCHEMA}.entity')
    log(f'loaded in {time.time() - started:.1f}s')


def degrees():
    """The degree distribution, and how much of it is past the cap.

    Counted per direction, because the cap is per direction: an entity with sixty subject relations
    and sixty object relations is under the cap twice, not over it once.
    """
    raw = psql(f"""
WITH d AS (
  SELECT subject_entity_id AS id, count(*) AS n FROM {SCHEMA}.fact
   WHERE scope='{PROJECT}' AND subject_entity_id IS NOT NULL GROUP BY 1
  UNION ALL
  SELECT object_entity_id, count(*) FROM {SCHEMA}.fact
   WHERE scope='{PROJECT}' AND object_entity_id IS NOT NULL GROUP BY 1)
SELECT count(*), max(n), percentile_disc(0.5) WITHIN GROUP (ORDER BY n),
       percentile_disc(0.99) WITHIN GROUP (ORDER BY n),
       count(*) FILTER (WHERE n > {FANOUT}), sum(n)
  FROM d""")
    nodes, top, median, p99, over, edges = (raw.split('|') + [''] * 6)[:6]
    return {'nodes': int(nodes or 0), 'max_degree': int(top or 0), 'median_degree': int(median or 0),
            'p99_degree': int(p99 or 0), 'nodes_past_the_cap': int(over or 0),
            'edge_ends': int(edges or 0), 'fanout_cap': FANOUT}


def anchor_plan(name):
    """EXPLAIN for the anchor lookup, at this size, on the statement recall actually sends.

    #102 asks whether the canonical-name/alias OR uses its index or degrades to a scan. A plan taken
    at a size where PostgreSQL would scan anyway proves nothing, which is why this runs per size.
    """
    plan = psql(f"""EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT entity_id FROM {SCHEMA}.entity
 WHERE scope='{PROJECT}' AND (normalized_name = '{name}' OR '{name}' = ANY(normalized_aliases))""", quiet=True)
    try:
        return json.loads(plan)[0]
    except Exception:
        return {'unavailable': 'the plan could not be read'}


# ── The read path ──────────────────────────────────────────────────────────────────────────────

def token():
    """A project and a write credential, through the bootstrap container an operator would use.

    The same two calls `vocabulary-coverage.py` makes, in the same order, because a second way of
    provisioning a project is a second thing that can be wrong about how this product is set up. The
    token is held in memory and never written under results.
    """
    created = subprocess.run(COMPOSE + ['run', '--rm', '-T', 'bootstrap', 'project', 'create', PROJECT],
                             capture_output=True, text=True)
    if created.returncode != 0 and 'already' not in created.stdout:
        raise SystemExit('create project: ' + created.stdout.strip()[-2000:])
    issued = subprocess.run(COMPOSE + ['run', '--rm', '-T', 'bootstrap', 'credential', 'issue',
                                       PROJECT + '_reader', '-project', PROJECT],
                            capture_output=True, text=True)
    # The bootstrap prints more than one token on a first run; this is the project's, named by its
    # prefix, and taking the first match indiscriminately would take the operator's.
    found = re.search(r'token: (tsk_[A-Za-z0-9_-]+)', issued.stdout)
    if issued.returncode != 0 or found is None:
        raise SystemExit('issue credential: exit %d' % issued.returncode)
    return found.group(1)


def recall(bearer, question):
    """One recall, timed, with the outcome classified rather than assumed."""
    body = json.dumps({'question': question}).encode()
    request = urllib.request.Request(API + '/v1/recalls', data=body, method='POST',
                                     headers={'Content-Type': 'application/json',
                                              'Authorization': 'Bearer ' + bearer})
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            payload = json.loads(response.read())
        return time.perf_counter() - started, response.status, '', payload.get('truncated', False), len(payload.get('facts') or [])
    except urllib.error.HTTPError as refused:
        raw = refused.read()
        code = ''
        try:
            code = json.loads(raw).get('error', {}).get('code', '')
        except Exception:
            pass
        return time.perf_counter() - started, refused.code, code, False, 0
    except Exception as broken:
        return time.perf_counter() - started, 0, type(broken).__name__, False, 0


def drive(bearer, questions, concurrency, requests):
    """`requests` recalls at `concurrency`, recording every outcome."""
    latencies, statuses, codes = [], {}, {}
    truncated = 0
    facts = 0
    lock = threading.Lock()
    counter = {'next': 0}
    started = time.time()

    def worker():
        nonlocal truncated, facts
        while True:
            with lock:
                index = counter['next']
                if index >= requests:
                    return
                counter['next'] = index + 1
            took, status, code, cut, found = recall(bearer, questions[index % len(questions)])
            with lock:
                latencies.append(took)
                statuses[status] = statuses.get(status, 0) + 1
                if code:
                    codes[code] = codes.get(code, 0) + 1
                truncated += 1 if cut else 0
                facts += found

    threads = [threading.Thread(target=worker) for _ in range(concurrency)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    elapsed = time.time() - started
    ordered = sorted(latencies)

    def at(q):
        if not ordered:
            return None
        return round(ordered[min(len(ordered) - 1, int(q * len(ordered)))], 4)

    return {
        'concurrency': concurrency, 'requests': requests, 'elapsed_seconds': round(elapsed, 2),
        'requests_per_second': round(len(ordered) / elapsed, 2) if elapsed else None,
        'p50_seconds': at(0.50), 'p95_seconds': at(0.95), 'p99_seconds': at(0.99),
        'max_seconds': round(ordered[-1], 4) if ordered else None,
        # Beside the percentiles, never under them: a throughput number built from failures is a
        # throughput number for failing.
        'by_status': {str(k): v for k, v in sorted(statuses.items())},
        'refusals_by_code': codes,
        'bundles_truncated': truncated,
        'facts_returned': facts,
    }


def connections():
    """What the deployment was holding while this ran."""
    raw = psql("""SELECT (SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend'),
        current_setting('max_connections')::int, current_setting('superuser_reserved_connections')::int""")
    used, ceiling, reserved = (raw.split('|') + ['', '', ''])[:3]
    return {'client_backends': int(used or 0), 'max_connections': int(ceiling or 0),
            'superuser_reserved_connections': int(reserved or 0)}


def main():
    random.seed(SEED)
    bearer = token()
    runs = []
    for size in SIZES:
        load(size)
        shape = degrees()
        # Anchored on a hub and on an ordinary node, because the cap binding and not binding are two
        # different measurements and #7 asks for both.
        questions = {
            'a hub past the cap': 'What about Entity 0?',
            'a node at the cap': 'What about Entity 1?',
            'an ordinary node': 'What about Entity 500?',
        }
        measured = {}
        for label, question in questions.items():
            measured[label] = [drive(bearer, [question], c, REQUESTS) for c in CONCURRENCIES]
        runs.append({
            'facts': size, 'graph': shape,
            'anchor_plan': anchor_plan('entity 0'),
            'connections_after': connections(),
            'recall': measured,
        })
        (RESULTS / 'readpath-scale.json').write_text(json.dumps({
            'project': PROJECT, 'seed': SEED, 'synthetic': True, 'runs': runs}, indent=2))
        log(f'{size} facts: max degree {shape["max_degree"]}, '
            f'{shape["nodes_past_the_cap"]} nodes past the cap of {FANOUT}')
    render(runs)
    return 0


def render(runs):
    """A table an operator reads, beside the JSON a script reads."""
    lines = ['# Read path at size — synthetic graph', '',
             'Synthetic facts written directly, so these measure the read path against a graph of a',
             'given size and shape and say nothing about extraction or answer quality. No latency',
             'target is claimed: none is agreed for this product.', '']
    for run in runs:
        g = run['graph']
        lines += [f'## {run["facts"]} facts', '',
                  f'{g["nodes"]} node-directions, median degree {g["median_degree"]}, p99 '
                  f'{g["p99_degree"]}, max {g["max_degree"]}, {g["nodes_past_the_cap"]} past the cap '
                  f'of {g["fanout_cap"]}.', '',
                  '| Anchor | Concurrency | p50 | p95 | p99 | req/s | refused | truncated |',
                  '|---|---:|---:|---:|---:|---:|---:|---:|']
        for label, series in run['recall'].items():
            for r in series:
                refused = sum(v for k, v in r['by_status'].items() if k != '200')
                lines.append(f'| {label} | {r["concurrency"]} | {r["p50_seconds"]} | '
                             f'{r["p95_seconds"]} | {r["p99_seconds"]} | {r["requests_per_second"]} '
                             f'| {refused} | {r["bundles_truncated"]} |')
        lines.append('')
    (RESULTS / 'readpath-scale.md').write_text('\n'.join(lines) + '\n')


if __name__ == '__main__':
    sys.exit(main())
