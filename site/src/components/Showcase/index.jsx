// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The journey, as a terminal session beside what it does inside.
//
// The requests and responses are the shapes the v1 contract documents, trimmed with "…" where a
// response carries more than fits; this is a replay, not a live session, and the component says so.
// The panel shows what each step changes: the observation log, formation's quoted claims, the entity
// graph, recall walking from its anchor to the evidence, and erasure ending at a counted residual.
//
// Reduced motion gets every frame finished, with no typing and no movement; the steps are buttons,
// so the whole story can be read one step at a time without the animation.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import clsx from 'clsx';
import styles from './styles.module.css';

const AUTH = '-H "Authorization: Bearer $TOKEN"';

const STEPS = [
  {
    label: 'observe',
    command: `curl -X POST $TAISCE/v1/observations ${AUTH} \\\n  -d '{"data_subject_id":"alice","messages":[{"role":"user",\n       "content":"I work at Ensera and I live in Dublin."}]}'`,
    output: `{"id":"0f3c…","scope":"default","log_offset":0}`,
    caption: 'The turn is stored and acknowledged at once. Nothing waits for a model.',
  },
  {
    label: 'freshness',
    command: `curl $TAISCE/v1/freshness ${AUTH}`,
    output: `{"scope":"default","stored":0,"formed":null,"parked":0}`,
    caption: 'Stored is not formed. A worker is asking the model what each message asserts.',
  },
  {
    label: 'formed',
    command: `curl $TAISCE/v1/freshness ${AUTH}`,
    output: `{"scope":"default","stored":0,"formed":0,"parked":0}`,
    caption: 'Two claims formed, each pinned to the exact bytes it came from, on a graph of entities.',
  },
  {
    label: 'recall',
    command: `curl -X POST $TAISCE/v1/recalls ${AUTH} \\\n  -d '{"question":"What do we know about Ensera?"}'`,
    output: `{"anchors":[{"name":"Ensera","type":"organisation","matched":"ensera",…}],\n "facts":[{"predicate":"works_at","object":"Ensera",\n   "statement":"The speaker works at Ensera.",\n   "evidence":{"quote":"I work at Ensera","byte_start":0,"byte_end":16,…},…}],…}`,
    caption: 'Recall starts at the entity the question names and returns the words behind the claim.',
  },
  {
    label: 'erase',
    command: `curl -X POST $TAISCE/v1/erasures ${AUTH} \\\n  -d '{"data_subject_id":"alice","reason":"right to erasure"}'`,
    output: `{…,"deleted":{"observation":1,"fact":2,"entity":3,"chunk":1},\n "residual":{"fact":0,"entity":0,"chunk":0,"rejected_claim":0},"clean":true}`,
    caption: 'Erasure walks every projection and counts what is left. The count is zero.',
  },
];

const TYPE_MS = 14;
const HOLD_MS = 2600;
const LOOP_MS = 4200;

function reducedMotion() {
  return typeof window !== 'undefined' && window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// Colours JSON the way an editor would: keys, strings, numbers, and the literals true/false/null.
function highlight(text) {
  const parts = [];
  const pattern = /("(?:[^"\\]|\\.)*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?)/g;
  let last = 0;
  let match;
  let n = 0;
  while ((match = pattern.exec(text))) {
    if (match.index > last) parts.push(text.slice(last, match.index));
    if (match[1] && match[2]) parts.push(<span key={n++} className={styles.key}>{match[1]}</span>, match[2]);
    else if (match[1]) parts.push(<span key={n++} className={styles.str}>{match[1]}</span>);
    else if (match[3]) parts.push(<span key={n++} className={styles.lit}>{match[3]}</span>);
    else parts.push(<span key={n++} className={styles.num}>{match[4]}</span>);
    last = pattern.lastIndex;
  }
  if (last < text.length) parts.push(text.slice(last));
  return parts;
}

export default function Showcase() {
  const [step, setStep] = useState(0);
  const [typed, setTyped] = useState(0);
  const [answered, setAnswered] = useState(false);
  const [playing, setPlaying] = useState(true);
  const [still, setStill] = useState(false);
  const [visible, setVisible] = useState(true);
  const root = useRef(null);
  const screen = useRef(null);

  useEffect(() => {
    const quiet = reducedMotion();
    setStill(quiet);
    if (quiet) {
      setPlaying(false);
      setTyped(STEPS[0].command.length);
      setAnswered(true);
    }
  }, []);

  // Nothing animates off screen: a hidden animation is battery spent on nobody.
  useEffect(() => {
    if (!root.current || typeof IntersectionObserver === 'undefined') return undefined;
    const observer = new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting), {threshold: 0.15});
    observer.observe(root.current);
    return () => observer.disconnect();
  }, []);

  const running = playing && visible && !still;

  useEffect(() => {
    if (!running) return undefined;
    const current = STEPS[step];
    if (typed < current.command.length) {
      const t = setTimeout(() => setTyped((v) => Math.min(current.command.length, v + 2)), TYPE_MS);
      return () => clearTimeout(t);
    }
    if (!answered) {
      const t = setTimeout(() => setAnswered(true), 380);
      return () => clearTimeout(t);
    }
    const last = step === STEPS.length - 1;
    const t = setTimeout(() => {
      setStep(last ? 0 : step + 1);
      setTyped(0);
      setAnswered(false);
    }, last ? LOOP_MS : HOLD_MS);
    return () => clearTimeout(t);
  }, [running, step, typed, answered]);

  useEffect(() => {
    if (screen.current) screen.current.scrollTop = screen.current.scrollHeight;
  }, [step, typed, answered]);

  const jump = useCallback((to) => {
    setStep(to);
    setTyped(STEPS[to].command.length);
    setAnswered(true);
  }, []);

  const replay = useCallback(() => {
    setStep(0);
    setTyped(still ? STEPS[0].command.length : 0);
    setAnswered(still);
    setPlaying(!still);
  }, [still]);

  // What the panel shows follows how far the session has got, not the clock.
  const reached = (i) => step > i || (step === i && answered);
  const stored = reached(0) && !reached(4);
  const forming = reached(1) && !reached(2);
  const formed = reached(2) && !reached(4);
  const recalling = reached(3) && !reached(4);
  const erased = reached(4);

  return (
    <div className={styles.showcase} ref={root}>
      <div className={styles.terminal} aria-label="A replay of the journey over the HTTP API">
        <div className={styles.terminalHead}>
          <i />
          <i />
          <i />
          <span>agent@laptop — taisce · replay of the v1 contract</span>
        </div>
        <div className={styles.screen} ref={screen}>
          {STEPS.slice(0, step + 1).map((s, i) => {
            const shown = i < step ? s.command : s.command.slice(0, typed);
            const done = i < step || answered;
            return (
              <div key={s.label + i}>
                <span className={styles.prompt}>$ </span>
                <span className={styles.command}>{shown}</span>
                {i === step && !done && <span className={styles.caret} />}
                {done && <span className={styles.output}>{highlight(s.output)}</span>}
              </div>
            );
          })}
        </div>
      </div>

      <div className={styles.panel}>
        <div className={styles.panelHead}>
          <span>inside the instance</span>
          <div className={styles.stages} role="tablist" aria-label="Steps">
            {STEPS.map((s, i) => (
              <button
                key={s.label}
                type="button"
                role="tab"
                aria-selected={i === step}
                className={clsx(styles.stage, i < step && styles.stageDone, i === step && styles.stageNow)}
                onClick={() => {
                  setPlaying(false);
                  jump(i);
                }}>
                {s.label}
              </button>
            ))}
          </div>
        </div>

        <div className={styles.body}>
          <div className={styles.lane}>
            <div className={styles.laneTitle}>observation log</div>
            <div className={clsx(styles.message, (stored || erased) && styles.shown, erased && styles.gone)}>
              <span className={styles.offset}>offset 0 · user · alice</span>
              <span className={clsx(styles.quote, (forming || formed) && styles.quoteLit, recalling && styles.quoteCited)}>I work at Ensera</span>
              {' and '}
              <span className={clsx(styles.quote, (forming || formed) && styles.quoteLit)}>I live in Dublin</span>.
            </div>
            <div className={styles.formation}>
              {forming && (
                <>
                  <span className={styles.spinner} /> formation · extracting
                </>
              )}
              {formed && !forming && <>formation · 2 claims, each with its quote</>}
              {erased && <>erased · every projection walked</>}
            </div>
            <span className={clsx(styles.claim, formed && styles.claimShown)}>works_at</span>
            <span className={clsx(styles.claim, formed && styles.claimShown)}>lives_in</span>
          </div>

          <div className={styles.lane}>
            <div className={styles.laneTitle}>entity graph</div>
            <svg className={styles.graph} viewBox="0 0 260 150" role="img" aria-label="speaker works at Ensera and lives in Dublin">
              <path d="M58 76 C 110 40, 150 34, 196 38" className={clsx(styles.edge, formed && styles.edgeShown, recalling && styles.edgeWalked, erased && styles.hidden)} />
              <path d="M58 76 C 110 112, 150 118, 196 114" className={clsx(styles.edge, formed && styles.edgeShown, erased && styles.hidden)} />
              <text x="104" y="40" className={clsx(styles.edgeLabel, !formed && styles.hidden)}>works_at</text>
              <text x="108" y="128" className={clsx(styles.edgeLabel, !formed && styles.hidden)}>lives_in</text>
              {[
                {x: 44, y: 76, name: 'speaker', type: 'person', anchor: false},
                {x: 214, y: 38, name: 'Ensera', type: 'organisation', anchor: recalling},
                {x: 214, y: 114, name: 'Dublin', type: 'place', anchor: false},
              ].map((n) => (
                <g key={n.name} className={clsx(styles.node, n.anchor && styles.nodeAnchor, !formed && styles.hidden)}>
                  <circle cx={n.x} cy={n.y} r="17" className={styles.pulse} />
                  <circle cx={n.x} cy={n.y} r="17" className={styles.nodeCircle} />
                  <text x={n.x} y={n.y + 3} textAnchor="middle" className={styles.nodeText}>
                    {n.name.length > 7 ? n.name.slice(0, 6) : n.name}
                  </text>
                  <text x={n.x} y={n.y + 28} textAnchor="middle" className={styles.nodeType}>
                    {n.type}
                  </text>
                </g>
              ))}
            </svg>
            <div className={styles.evidence} style={{opacity: recalling ? 1 : 0}}>
              anchor Ensera → works_at → “I work at Ensera” · bytes 0–16
            </div>
          </div>
        </div>

        <div className={styles.meters}>
          <div className={styles.meter}>
            stored
            <b>{reached(0) ? '0' : '—'}</b>
          </div>
          <div className={clsx(styles.meter, formed && styles.meterGood)}>
            formed
            <b>{reached(2) ? '0' : reached(0) ? 'null' : '—'}</b>
          </div>
          <div className={clsx(styles.meter, erased && styles.meterGood)}>
            residual
            <b>{erased ? '0 · clean' : '—'}</b>
          </div>
        </div>

        <p className={styles.caption} aria-live="polite">
          {STEPS[step].caption}
        </p>
        <div className={styles.controls}>
          <div style={{display: 'flex', gap: 8}}>
            {!still && (
              <button type="button" className={styles.button} onClick={() => setPlaying((p) => !p)}>
                {playing ? 'pause' : 'play'}
              </button>
            )}
            <button type="button" className={styles.button} onClick={replay}>
              replay
            </button>
          </div>
          <span className={styles.note}>request and response shapes from the v1 contract</span>
        </div>
      </div>
    </div>
  );
}
