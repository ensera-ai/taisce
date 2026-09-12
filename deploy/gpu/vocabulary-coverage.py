# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""Measure how much of a document corpus the relation vocabulary admits.

The vocabulary is closed and anything outside it is refused as `unmapped_relation` rather than
admitted or dropped. Both rest on an assumption nothing has measured: that the set covers what a
real corpus asserts. This runs a corpus through the deployed write path and reads back, from the
database, what extraction admitted against everything it refused, with the refusals broken down by the
relation the model tried to use. The breakdown is the finding: an aggregate cannot tell a missing
predicate asserted five hundred times from five hundred relations asserted once.

What this deliberately reuses and what it does not. Ingestion goes through the public observation
route with the same admission, size and idempotency rules an adapter meets, so the number describes
the product's write path and not a shortcut beside it. Reading the result goes straight to the
database through the operator connection, because the refusal rows carry the model's own wording and
nothing on the API is meant to return them.

Sharding. Formation holds one advisory lock per project, so one project forms a corpus serially. The
corpus is dealt across `CORPUS_SHARDS` projects, one per generation replica in the qualification
topology, and the report joins across them by the shared data subject. The subject is synthetic: a
news corpus has no principal, and every first-person claim a tool-role message makes is refused by
role, which is the policy working rather than a defect.

Roles. A third-party document is observed under the `tool` role, which is the stored role recall
selects for fetched text (docs/12). It is never `user`, because a user message speaks for the subject.

The corpus itself is never written under results/ and never committed: the licence a benchmark
corpus ships under is the operator's to accept, and this file reads it from wherever they put it.
"""
import html
import json
import os
import re
import shlex
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
CORPUS = Path(os.environ['CORPUS'])
LIMIT = int(os.environ.get('CORPUS_LIMIT', '0'))
SHARDS = int(os.environ.get('CORPUS_SHARDS', '7'))
# Empty means no data subject on the observations. A document is nobody's turn, and a claim from a
# non-user message is refused by role whenever a subject is named, so a corpus observed under a
# subject yields refusals only. Without one, tool-role facts are admitted and counted.
SUBJECT = os.environ.get('CORPUS_SUBJECT', '')
PREFIX = os.environ.get('CORPUS_PROJECT_PREFIX', 'coverage')
SCHEMA = os.environ.get('TAISCE_SCHEMA', 'memory')
RESULTS = Path(os.environ.get('RESULTS', 'results'))
DEADLINE = float(os.environ.get('FORMATION_DEADLINE_SECONDS', '14400'))
STALL = float(os.environ.get('FORMATION_STALL_SECONDS', '1800'))
# D73: 65536 UTF-8 content bytes per message. A document over that is truncated at a code point
# boundary and counted, never split: a split would make two observations of one source.
MESSAGE_BYTES = 65536
KEY_NAMESPACE = uuid.uuid5(uuid.NAMESPACE_URL, 'https://taisce.dev/vocabulary-coverage')

if not re.fullmatch(r'[a-z][a-z0-9_]{0,30}', PREFIX):
    raise SystemExit('CORPUS_PROJECT_PREFIX must be a lowercase identifier')
if SUBJECT and not re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', SUBJECT):
    raise SystemExit('CORPUS_SUBJECT must be a plain identifier')

RESULTS.mkdir(parents=True, exist_ok=True)


def log(message):
    print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()) + ' ' + message, flush=True)


# ── Corpus ─────────────────────────────────────────────────────────────────────────────────────

_TAG = re.compile(r'<[^>]+>')
_WS = re.compile(r'[ \t\r\f\v]+')
_BLANK = re.compile(r'\n{3,}')


def nitf_to_text(body):
    """Flatten AP's NITF body to paragraphs. Tags carry layout, not assertions."""
    body = re.sub(r'</p\s*>', '\n\n', body, flags=re.I)
    body = re.sub(r'<br\s*/?>', '\n', body, flags=re.I)
    body = html.unescape(_TAG.sub('', body))
    body = _WS.sub(' ', body)
    return _BLANK.sub('\n\n', body).strip()


def bound(text, stats):
    data = text.encode('utf-8')
    if len(data) <= MESSAGE_BYTES:
        return text
    stats['truncated'] += 1
    cut = data[:MESSAGE_BYTES]
    while cut and (cut[-1] & 0xC0) == 0x80:
        cut = cut[:-1]
    return cut.decode('utf-8')


def load_corpus(stats):
    """Yield (key, occurred_at, text, source_name) for each document, in a stable order."""
    files = sorted(p for p in CORPUS.rglob('*') if p.is_file() and p.suffix in ('.json', '.txt', '.md'))
    if not files:
        raise SystemExit(f'no documents under {CORPUS}')
    if LIMIT:
        files = files[:LIMIT]
    for path in files:
        rel = path.relative_to(CORPUS).as_posix()
        if path.suffix == '.json':
            item = json.loads(path.read_text(encoding='utf-8-sig'))
            body = item.get('body_nitf') or item.get('body_text') or item.get('text') or ''
            if not body.strip():
                stats['empty'] += 1
                continue
            headline = item.get('headline') or item.get('title') or ''
            text = (headline.strip() + '\n\n' + nitf_to_text(body)).strip()
            ident = (item.get('altids') or {}).get('itemid') or rel
            occurred = item.get('firstcreated') or item.get('versioncreated')
        else:
            text = path.read_text(encoding='utf-8').strip()
            if not text:
                stats['empty'] += 1
                continue
            ident, occurred = rel, None
        yield str(uuid.uuid5(KEY_NAMESPACE, ident)), occurred, bound(text, stats), rel


# ── Deployment ─────────────────────────────────────────────────────────────────────────────────

def compose(*args, input_text=None):
    return subprocess.run(COMPOSE + list(args), text=True, input=input_text,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=600)


def provision(shard):
    """One project and one writer credential per shard. The token is held in memory only."""
    name = f'{PREFIX}_{shard}'
    created = compose('run', '--rm', '-T', 'bootstrap', 'project', 'create', name)
    if created.returncode != 0 and 'already' not in created.stdout:
        raise RuntimeError(f'create project {name}: {created.stdout.strip()}')
    issued = compose('run', '--rm', '-T', 'bootstrap', 'credential', 'issue', f'{name}_writer', '-project', name)
    match = re.search(r'token: (tsk_[A-Za-z0-9_-]+)', issued.stdout)
    if issued.returncode != 0 or match is None:
        raise RuntimeError(f'issue credential for {name}: exit {issued.returncode}')
    return name, match.group(1)


def request(token, path, body=None, timeout=35):
    req = urllib.request.Request(API + path, data=None if body is None else json.dumps(body).encode(),
                                 headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=timeout) as response:
        return json.load(response)


def observe(token, key, occurred, text):
    """Admission refusals (429, 503) are the backlog working; retry the same key, never a new one."""
    # A tool message must state its group: roles alone cannot pair a result with its call, and a
    # whole document is one unit, so it is group zero on its own.
    body = {'idempotency_key': key, 'messages': [{'group_ordinal': 0, 'role': 'tool', 'content': text}]}
    if SUBJECT:
        body['data_subject_id'] = SUBJECT
    if occurred:
        body['occurred_at'] = occurred
    delay = 1.0
    while True:
        try:
            return request(token, '/v1/observations', body)
        except urllib.error.HTTPError as error:
            if error.code in (429, 503):
                time.sleep(float(error.headers.get('Retry-After') or delay))
                delay = min(delay * 2, 30)
                continue
            detail = error.read().decode('utf-8', 'replace')[:300]
            raise RuntimeError(f'observe {key}: HTTP {error.code} {detail}') from None
        except (urllib.error.URLError, TimeoutError):
            time.sleep(delay)
            delay = min(delay * 2, 30)


# ── Run ────────────────────────────────────────────────────────────────────────────────────────

def ingest(shards, docs):
    """Deal documents across shards and post each shard's share on its own thread."""
    dealt = [[] for _ in shards]
    for index, doc in enumerate(docs):
        dealt[index % len(shards)].append(doc)
    offsets = [None] * len(shards)
    ledger = []
    errors = []
    lock = threading.Lock()

    def worker(i):
        name, token = shards[i]
        for key, occurred, text, rel in dealt[i]:
            try:
                out = observe(token, key, occurred, text)
            except RuntimeError as error:
                with lock:
                    errors.append(str(error))
                continue
            with lock:
                ledger.append({'shard': name, 'source': rel, 'observation_id': out['id'],
                               'log_offset': out['log_offset']})
                offsets[i] = out['log_offset'] if offsets[i] is None else max(offsets[i], out['log_offset'])

    threads = [threading.Thread(target=worker, args=(i,), daemon=True) for i in range(len(shards))]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    return ledger, offsets, errors


def await_formation(shards, offsets):
    """Wait for every shard's watermark to pass its last offset, or for a stall to be declared."""
    started = time.monotonic()
    progress = {}
    last_change = time.monotonic()
    while True:
        pending = []
        for i, (name, token) in enumerate(shards):
            if offsets[i] is None:
                continue
            fresh = request(token, '/v1/freshness')
            formed = fresh.get('formed')
            parked = fresh.get('parked', 0)
            state = (formed, parked)
            if progress.get(name) != state:
                progress[name] = state
                last_change = time.monotonic()
            if formed is None or formed < offsets[i]:
                pending.append((name, formed, offsets[i], parked))
        if not pending:
            return {'complete': True, 'seconds': round(time.monotonic() - started, 1)}
        now = time.monotonic()
        if now - started > DEADLINE or now - last_change > STALL:
            return {'complete': False, 'seconds': round(now - started, 1),
                    'pending': [{'shard': n, 'formed': f, 'target': t, 'parked': p} for n, f, t, p in pending]}
        time.sleep(5)


def sql(query):
    out = compose('exec', '-T', 'postgres', 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'taisce',
                  '-At', '-c', query)
    if out.returncode != 0:
        raise RuntimeError('report query failed: ' + out.stdout.strip()[-800:])
    text = out.stdout.strip()
    return json.loads(text) if text else []


def report(stats, ledger, formation, errors, elapsed):
    s = SCHEMA
    scopes = ', '.join("'%s'" % (f'{PREFIX}_{i}') for i in range(SHARDS))
    where = f"scope IN ({scopes})"
    observations = sql(f"""SELECT json_agg(t) FROM (
        SELECT count(*) AS total, count(formed_at) AS formed, count(parked_at) AS parked,
               count(formation_failed_at) AS with_failed_attempt, coalesce(sum(formation_attempts),0) AS attempts
          FROM {s}.observation WHERE {where}) t""")[0]
    facts_by_predicate = sql(f"""SELECT coalesce(json_agg(t), '[]') FROM (
        SELECT f.predicate, f.source_role, count(DISTINCT f.fact_id) AS facts
          FROM {s}.fact f JOIN {s}.fact_evidence e ON e.fact_id = f.fact_id
          JOIN {s}.observation o ON o.observation_id = e.source_observation_id
         WHERE o.{where} GROUP BY 1, 2 ORDER BY 3 DESC) t""")
    rejected_by_reason = sql(f"""SELECT coalesce(json_agg(t), '[]') FROM (
        SELECT reason, count(*) AS claims FROM {s}.rejected_claim WHERE {where}
         GROUP BY 1 ORDER BY 2 DESC) t""")
    unmapped = sql(f"""SELECT coalesce(json_agg(t), '[]') FROM (
        SELECT lower(btrim(predicate)) AS predicate, count(*) AS claims,
               min(statement) AS sample_statement
          FROM {s}.rejected_claim WHERE {where} AND reason = 'unmapped_relation'
         GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 300) t""")
    per_document = sql(f"""SELECT json_agg(t) FROM (
        SELECT count(*) FILTER (WHERE facts = 0) AS documents_with_no_fact,
               percentile_cont(0.5) WITHIN GROUP (ORDER BY facts) AS median_facts,
               max(facts) AS max_facts, sum(facts) AS facts
          FROM (SELECT o.observation_id, count(DISTINCT e.fact_id) AS facts
                  FROM {s}.observation o
                  LEFT JOIN {s}.fact_evidence e ON e.source_observation_id = o.observation_id
                 WHERE o.{where} GROUP BY 1) d) t""")[0]

    facts = sum(row['facts'] for row in facts_by_predicate)
    rejected = {row['reason']: row['claims'] for row in rejected_by_reason}
    refused = sum(rejected.values())
    proposals = facts + refused
    unmapped_total = rejected.get('unmapped_relation', 0)
    result = {
        'corpus': str(CORPUS), 'documents_offered': stats['offered'], 'documents_empty': stats['empty'],
        'documents_truncated': stats['truncated'], 'documents_observed': len(ledger),
        'ingest_errors': errors[:50], 'shards': SHARDS, 'subject': SUBJECT,
        'observations': observations, 'formation': formation, 'elapsed_seconds': round(elapsed, 1),
        'proposals': proposals, 'facts': facts, 'refused': refused, 'refused_by_reason': rejected,
        'unmapped_rate': round(unmapped_total / proposals, 4) if proposals else None,
        'unmapped_share_of_refusals': round(unmapped_total / refused, 4) if refused else None,
        'per_document': per_document, 'facts_by_predicate': facts_by_predicate,
        'unmapped_predicates': unmapped,
        'limits': ['Claims refused by role are counted in formation reports and not persisted, so '
                   'they are absent from the denominator.',
                   'A truncated document loses its tail; the count is recorded above.'],
    }
    (RESULTS / 'vocabulary-coverage.json').write_text(json.dumps(result, indent=2))
    lines = [f"# Vocabulary coverage — {CORPUS.name}", '',
             f"documents observed {len(ledger)} / offered {stats['offered']}; "
             f"formed {observations['formed']}, parked {observations['parked']}, with a failed attempt {observations['with_failed_attempt']}",
             f"proposals {proposals}: facts {facts}, refused {refused}",
             f"unmapped rate {result['unmapped_rate']} (unmapped {unmapped_total}); share of refusals {result['unmapped_share_of_refusals']}",
             f"documents with no fact {per_document['documents_with_no_fact']}, median facts {per_document['median_facts']}",
             '', '| reason | claims |', '|---|---|']
    lines += [f"| {r['reason']} | {r['claims']} |" for r in rejected_by_reason]
    lines += ['', '| unmapped predicate | claims | sample |', '|---|---|---|']
    lines += [f"| {r['predicate']} | {r['claims']} | {r['sample_statement'][:120].replace('|', '/')} |" for r in unmapped[:60]]
    lines += ['', '| admitted predicate | role | facts |', '|---|---|---|']
    lines += [f"| {r['predicate']} | {r['source_role']} | {r['facts']} |" for r in facts_by_predicate]
    (RESULTS / 'vocabulary-coverage.md').write_text('\n'.join(lines) + '\n')
    for line in lines[:6]:
        log(line)
    return result


def main():
    started = time.monotonic()
    stats = {'offered': 0, 'empty': 0, 'truncated': 0}
    docs = list(load_corpus(stats))
    stats['offered'] = len(docs) + stats['empty']
    log(f'corpus: {len(docs)} documents from {CORPUS} (empty {stats["empty"]}, truncated {stats["truncated"]})')
    shards = [provision(i) for i in range(SHARDS)]
    log(f'provisioned {len(shards)} projects')
    ledger, offsets, errors = ingest(shards, docs)
    log(f'observed {len(ledger)} documents, {len(errors)} ingest errors')
    (RESULTS / 'vocabulary-coverage-ledger.json').write_text(json.dumps(ledger))
    formation = await_formation(shards, offsets)
    log(f'formation {"complete" if formation["complete"] else "INCOMPLETE"} after {formation["seconds"]}s')
    result = report(stats, ledger, formation, errors, time.monotonic() - started)
    if not formation['complete'] or errors:
        sys.exit(1)
    if result['proposals'] == 0:
        log('no proposals were made; the extractor produced nothing to measure')
        sys.exit(1)


if __name__ == '__main__':
    main()
