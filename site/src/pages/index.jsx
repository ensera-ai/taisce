// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The landing page. Every claim on it is one the code holds, and every package name and command is
// the one that exists in the repository it names.

import React from 'react';
import clsx from 'clsx';
import Layout from '@theme/Layout';
import Link from '@docusaurus/Link';
import {useBaseUrlUtils} from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import useBrokenLinks from '@docusaurus/useBrokenLinks';
import Showcase from '@site/src/components/Showcase';
import CONTRIBUTORS from '@site/src/data/contributors';
import styles from './index.module.css';

const JOURNEY = [
  {
    state: 'save',
    title: 'Your agent sends what happened',
    text: 'A turn is saved straight away and you get its position back. Saving never waits for a model, so memory never slows your agent down.',
    code: 'POST /v1/observations',
  },
  {
    state: 'learn',
    title: 'Taisce turns it into facts',
    text: 'In the background, a model reads each message and pulls out facts. Every fact keeps the exact words it came from, or it is thrown away.',
    code: 'stored → formed',
  },
  {
    state: 'recall',
    title: 'Ask, and get facts with their words',
    text: 'A question starts at the people and things it names and follows the facts around them. You get each fact with the quote behind it.',
    code: 'POST /v1/recalls',
  },
  {
    state: 'forget',
    title: 'Forget someone, and see the proof',
    text: 'Erase a person and Taisce removes everything derived from what they said, then counts what is left. The count you get back is zero.',
    code: 'POST /v1/erasures',
  },
];

// The same six capabilities, in the same order and substance, as Taisce's page on ensera.ai. A change
// to one of them is a change to both; a reader who meets the two pages should not have to reconcile
// them.
const PROPERTIES = [
  {n: '01', title: 'Answers from the things a question names', text: 'Recall starts at the people and things your question names and follows the facts around them, instead of searching for similar sentences. It never calls a model, so a slow model delays new memories, not answers.', code: 'anchors → facts'},
  {n: '02', title: 'Every fact shows its words', text: 'Each fact carries the exact quote it came from and where that quote sits in the stored message. A claim whose words are not there is refused before it is stored.', code: 'quote · byte_start · byte_end'},
  {n: '03', title: 'Two clocks on every fact', text: 'When something was true, and when Taisce learned it. A new job supersedes the old one without erasing it, so you can ask what was true then, or what was known then.', code: 'as_of · as_known_at'},
  {n: '04', title: 'Forgetting you can count', text: 'Erasing a person removes everything derived from what they said, then recounts what still matches them in the same transaction. The receipt says what was deleted and what is left.', code: 'deleted · residual · clean'},
  {n: '05', title: 'Memory reaches the model as data', text: 'Every adapter hands memory over as one user message marked untrusted, never as a system instruction, and one conformance suite holds Python, .NET and Java to it.', code: 'taisce-memory/v1 untrusted'},
  {n: '06', title: 'Just PostgreSQL, on your infrastructure', text: 'No vector database, graph engine or queue to run: one database to operate, back up and erase from. All of it is Apache-2.0.', code: 'docker compose up'},
];

// What no agent-memory product surveyed on 2026-09-13 documents (ensera-ai/taisce#30). Properties that
// other products also have — two clocks, recall without a model, memory handed over as a user message —
// are capabilities above and deliberately not claimed here, because a difference that is not one is
// the first thing a reader comparing products checks.
const DIFFERENT = [
  {k: 'evidence', title: 'Evidence to the byte, or no fact', text: "A fact is stored only with the quote that supports it and that quote's position in the message. You check an answer by reading the words, not by trusting the extraction.", to: '/docs/08-citation-resolution'},
  {k: 'erasure', title: 'A receipt that counts what is left', text: 'Erasure returns what it deleted and a recount of what still matches the person, taken in the same transaction. A clean receipt is a count of zero, not a status message.', to: '/docs/examples/forget-a-person'},
  {k: 'substrate', title: 'Entities and time on one database', text: 'Entity-anchored, two-clock recall with no graph database or vector database beside PostgreSQL, and no licensed tier for either. One place data lives is one place an erasure has to reach.', to: '/docs/postgresql/overview'},
  {k: 'adapters', title: 'One contract for every adapter', text: 'Python, .NET and Java adapters pass the same live conformance suite: memory enters as untrusted data, a failed recall never stops the turn, a failed save is never silent.', to: '/docs/developers/adapters#the-conformance-suite'},
];

const ADAPTERS = [
  {lang: 'Python', seam: 'Microsoft Agent Framework · LangGraph', pkg: 'taisce-agent-framework · taisce-langgraph', to: '/docs/developers/python'},
  {lang: '.NET', seam: 'Microsoft Agent Framework', pkg: 'Taisce.AgentFramework', to: '/docs/developers/dotnet'},
  {lang: 'Java', seam: 'Spring AI · LangChain4j', pkg: 'taisce-spring-ai · taisce-langchain4j', to: '/docs/developers/java'},
  {lang: 'MCP', seam: 'Claude Code and other coding agents', pkg: 'POST /mcp', to: '/docs/developers/mcp'},
  {lang: 'HTTP', seam: 'Any language, plain JSON over HTTP', pkg: '/v1/…', to: '/docs/developers/http-api'},
  {lang: 'CLI', seam: 'Projects, keys, health and the audit log', pkg: 'taisce', to: '/docs/developers/cli'},
];

const DEEPER = [
  {k: 'examples', title: 'Examples', text: 'Short recipes: remember a preference, answer with evidence, forget a person.', to: '/docs/examples/overview'},
  {k: 'architecture', title: 'How it is built', text: 'Saving, learning, recalling and forgetting, step by step, and how it is secured.', to: '/docs/architecture/overview'},
  {k: 'postgresql', title: 'The database layer', text: 'Tables, roles, concurrency, indexes and query plans.', to: '/docs/postgresql/overview'},
  {k: 'reference', title: 'Every line, generated', text: 'Every package, function, test, table and migration, straight from the code.', to: '/docs/reference/code/overview'},
];

function initials(name) {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((word) => word[0].toUpperCase())
    .join('');
}

function Different() {
  const {siteConfig} = useDocusaurusContext();
  const repository = siteConfig.customFields.repository;
  return (
    <section className={styles.different}>
      <div className={styles.wrap}>
        <span className={styles.kickerDark}>what is different</span>
        <h2 className={styles.h2}>Proof you can run, not a promise.</h2>
        <p className={styles.sectionPDark}>
          Recalling entities, keeping time and answering without a model are what good agent memory does. These four are
          what Taisce adds, and each one is a field in a response or a test you can run yourself.
        </p>
        <div className={styles.diffGrid}>
          {DIFFERENT.map((d) => (
            <Link key={d.k} to={d.to} className={styles.diffCard}>
              <span className={styles.diffK}>{d.k}</span>
              <h3>{d.title}</h3>
              <p>{d.text}</p>
              <span className={styles.cardGo}>See it →</span>
            </Link>
          ))}
        </div>
        <p className={styles.diffNote}>
          Compared on 2026-09-13 with the agent-memory products whose documentation we could read. The survey, with every
          source and what it could not confirm, is <a href={`https://github.com/${repository}/issues/30`}>issue #30</a>.
        </p>
      </div>
    </section>
  );
}

function Contributors() {
  const {withBaseUrl} = useBaseUrlUtils();
  const {siteConfig} = useDocusaurusContext();
  const repository = siteConfig.customFields.repository;
  // The footer links here from every page. The build checks anchors, and only knows the ones a
  // component registers; an id on a section is not registered by itself.
  useBrokenLinks().collectAnchor('contributors');
  return (
    <section className={styles.contributors} id="contributors">
      <div className={styles.wrap}>
        <span className={styles.kickerDark}>contributors</span>
        <h2 className={styles.h2}>The people building Taisce.</h2>
        <p className={styles.sectionPDark}>
          Built by Ensera and shaped by everyone who sends a fix, a test, a page of documentation or a good question. Your
          name belongs here too: add it with your first pull request.
        </p>
        <div className={styles.people}>
          {CONTRIBUTORS.map((person) => (
            <a key={person.github} className={styles.person} href={`https://github.com/${person.github}`}>
              {person.image ? (
                <img src={withBaseUrl(person.image)} alt="" width="56" height="56" />
              ) : (
                <span className={styles.initials} aria-hidden="true">
                  {initials(person.name)}
                </span>
              )}
              <span className={styles.personName}>{person.name}</span>
              <span className={styles.personLogin}>@{person.github}</span>
            </a>
          ))}
          <a className={clsx(styles.person, styles.personOpen)} href={`https://github.com/${repository}/blob/main/CONTRIBUTING.md`}>
            <span className={styles.initials} aria-hidden="true">
              +
            </span>
            <span className={styles.personName}>You, next</span>
            <span className={styles.personLogin}>how to contribute →</span>
          </a>
        </div>
      </div>
    </section>
  );
}

export default function Home() {
  return (
    <Layout title="Memory for AI agents" description="Open-source memory for AI agents on PostgreSQL: facts with their evidence, time on every fact, and forgetting you can prove.">
      <main className={styles.page}>
        <section className={styles.hero}>
          <div className={styles.wrap}>
            <div className={styles.heroGrid}>
              <div>
                <span className={styles.signal}>open source · Apache-2.0 · built by Ensera</span>
                <h1 className={styles.title}>
                  taisce<span>.</span>
                </h1>
                <p className={styles.tag}>What can remember — and prove it forgot?</p>
                <p className={styles.lede}>
                  Memory for your AI agents. Your agent tells Taisce what happened; later it asks a question and gets back
                  facts, each with the exact words it came from and when it was true. When someone asks to be forgotten, you
                  get a count of what is left, and it is zero.
                </p>
                <div className={styles.actions}>
                  <Link className={styles.primary} to="/docs/developers/quickstart">
                    Start in the quickstart
                  </Link>
                  <Link className={styles.secondary} to="/docs/examples/overview">
                    See examples
                  </Link>
                </div>
                <div className={styles.meta}>
                  <span>
                    <b>PostgreSQL</b> only
                  </span>
                  <span>
                    <b>Python · Java · .NET</b>
                  </span>
                  <span>
                    <b>MCP</b> for coding agents
                  </span>
                  <span>
                    runs on <b>your</b> infrastructure
                  </span>
                </div>
              </div>
              <Showcase />
            </div>
          </div>
        </section>

        <section className={styles.proof}>
          <div className={styles.wrap}>
            <div className={styles.proofGrid}>
              <div>
                <span className={styles.kicker}>why taisce</span>
                <h2 className={styles.h2}>Memory that shows its work.</h2>
                <p className={styles.sectionP}>
                  Six things your agent gets that a pile of stored text can't give it.{' '}
                  <Link to="/docs/start/how-it-works">How it works →</Link>
                </p>
              </div>
              <div className={styles.featureList}>
                {PROPERTIES.map((p) => (
                  <div key={p.n} className={styles.feature}>
                    <span className={styles.featureN}>{p.n}</span>
                    <div>
                      <h3>{p.title}</h3>
                      <p>{p.text}</p>
                    </div>
                    <code>{p.code}</code>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </section>

        <Different />

        <section className={styles.architecture}>
          <div className={styles.wrap}>
            <div className={styles.archGrid}>
              <div>
                <span className={styles.kickerDark}>how it flows</span>
                <h2 className={styles.h2}>One memory, start to finish.</h2>
                <p className={styles.sectionPDark}>
                  Saving is instant; learning happens in the background. A freshness check tells your agent how far behind
                  its memory is.
                </p>
                <div className={styles.compose}>
                  <code>docker compose up</code>
                  <span>Starts PostgreSQL and the service from published images, and prints your first tokens.</span>
                </div>
              </div>
              <div className={styles.pipeline}>
                {JOURNEY.map((j) => (
                  <div key={j.state} className={styles.pipeRow}>
                    <span className={styles.pipeState}>{j.state}</span>
                    <div>
                      <h3>{j.title}</h3>
                      <p>{j.text}</p>
                    </div>
                    <code>{j.code}</code>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </section>

        <section className={styles.developers}>
          <div className={styles.wrap}>
            <span className={styles.kickerDark}>for developers</span>
            <h2 className={styles.h2}>Plug it into what you already use.</h2>
            <p className={styles.sectionPDark}>
              Adapters add memory to your agent framework in a few lines. Before each answer they fetch what's known; after,
              they save the turn. Every adapter passes the same test suite.
            </p>
            <div className={styles.cards}>
              {ADAPTERS.map((a) => (
                <Link key={a.lang} to={a.to} className={styles.card}>
                  <span className={styles.cardLang}>{a.lang}</span>
                  <span className={styles.cardSeam}>{a.seam}</span>
                  <code>{a.pkg}</code>
                  <span className={styles.cardGo}>Read the guide →</span>
                </Link>
              ))}
            </div>
          </div>
        </section>

        <section className={styles.deeper}>
          <div className={styles.wrap}>
            <span className={styles.kicker}>go further</span>
            <h2 className={styles.h2}>From a first example to the last index.</h2>
            <div className={styles.deeperGrid}>
              {DEEPER.map((d) => (
                <Link key={d.k} to={d.to} className={styles.deeperCard}>
                  <span className={styles.featureN}>{d.k}</span>
                  <h3>{d.title}</h3>
                  <p>{d.text}</p>
                </Link>
              ))}
            </div>
          </div>
        </section>

        <Contributors />
      </main>
    </Layout>
  );
}
