// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// Parses every ```mermaid block in the documentation with the Mermaid this site renders with, and
// fails naming the file and line of every block that does not parse (#39).
//
// Mermaid is parsed in the reader's browser, so a diagram that does not parse builds cleanly and
// fails for whoever opens the page: a flowchart with a node named `graph` was first found that way,
// on the page a new reader opens first (#38). The link and anchor checks refuse a broken page at build
// time; this refuses a broken diagram at the same point.
//
// ── WHY DOMPURIFY IS STUBBED ─────────────────────────────────────────────────────────────────────
//
// Mermaid calls DOMPurify when it loads, and DOMPurify needs a browser window. Parsing needs neither:
// it reads the diagram's grammar and renders nothing. So the two methods Mermaid reaches for are
// replaced before it loads, rather than adding a browser emulation to the build.
//
// What this does not cover: a diagram that parses and renders wrongly — overlapping labels, a layout
// nobody can read. That is a rendering question, and only a browser answers it.
//
// ── WHY IT CHECKS ITSELF FIRST ────────────────────────────────────────────────────────────────────
//
// A check that cannot refuse proves nothing, and a Mermaid upgrade that made parse() lenient, or a
// stub that swallowed errors, would pass every document silently. So before reading the documents it
// parses the diagram that broke #38 and a known-good one, and stops unless the first is refused and
// the second accepted.
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

const purify = (await import("dompurify")).default;
if (typeof purify.addHook !== "function") purify.addHook = () => {};
if (typeof purify.sanitize !== "function") purify.sanitize = (text) => text;
const { default: mermaid } = await import("mermaid");
mermaid.initialize({ startOnLoad: false });

async function parses(text) {
  try {
    await mermaid.parse(text);
    return null;
  } catch (err) {
    return String(err?.message ?? err).split("\n")[0];
  }
}

const refusedByDesign = "flowchart LR\n    graph --> end";
const acceptedByDesign = 'flowchart LR\n    reader["reader"] --> page["page"]';
if ((await parses(refusedByDesign)) === null || (await parses(acceptedByDesign)) !== null) {
  console.error("check-mermaid: the parser did not refuse the #38 diagram and accept a good one, so it can check nothing");
  process.exit(2);
}

async function markdown(dir) {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...(await markdown(full)));
    else if (entry.name.endsWith(".md")) out.push(full);
  }
  return out;
}

const root = path.resolve(process.argv[2] ?? "../docs");
let blocks = 0;
const failures = [];
for (const file of (await markdown(root)).sort()) {
  const lines = (await readFile(file, "utf8")).split("\n");
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].trim() !== "```mermaid") continue;
    const start = i + 1;
    const body = [];
    for (i++; i < lines.length && lines[i].trim() !== "```"; i++) body.push(lines[i]);
    blocks++;
    const refusal = await parses(body.join("\n"));
    if (refusal !== null) failures.push(`${path.relative(process.cwd(), file)}:${start}: ${refusal}`);
  }
}

for (const failure of failures) console.error(failure);
if (failures.length > 0) {
  console.error(`check-mermaid: ${failures.length} of ${blocks} diagrams do not parse`);
  process.exit(1);
}
console.log(`check-mermaid: ${blocks} diagrams parse`);
