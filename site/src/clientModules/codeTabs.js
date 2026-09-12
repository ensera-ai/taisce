// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// Language tabs from adjacent code blocks.
//
// A document shows one call in several languages by writing the fenced blocks back to back. On
// GitHub they read as a sequence, which is correct there; here they read as one block with a tab per
// language, and the language a reader picks is remembered across pages. Only languages that are
// alternatives to each other are grouped, so a command followed by its JSON output stays two blocks.
//
// Two things the page does shape how this works:
//
//   - React owns the code blocks, so none is ever moved. The tab bar is a node of its own placed
//     before the first block, and the blocks are only hidden or shown. Moving them into a wrapper
//     crashed the page on the next render, when React went to remove nodes from a parent they were
//     no longer in.
//   - React can replace the code blocks after this runs. So a bar never keeps references to them: it
//     finds the blocks that follow it whenever it needs them, and a watcher re-applies each bar's
//     choice when the blocks under it change.
//
// The documents stay plain markdown: nothing in them is written for this site.

const STORAGE_KEY = 'taisce.code-language';

const LABELS = {
  bash: 'curl',
  shell: 'Shell',
  python: 'Python',
  typescript: 'TypeScript',
  javascript: 'JavaScript',
  go: 'Go',
  java: 'Java',
  csharp: 'C#',
  xml: 'Maven',
  kotlin: 'Gradle (Kotlin)',
  groovy: 'Gradle (Groovy)',
};

let watcher = null;

function language(block) {
  const match = block.className.match(/language-([\w-]+)/);
  return match ? match[1] : null;
}

function isCodeBlock(element) {
  return Boolean(element && element.classList && element.classList.contains('theme-code-block'));
}

function runFrom(first) {
  const run = [];
  let node = first;
  while (isCodeBlock(node)) {
    run.push(node);
    node = node.nextElementSibling;
  }
  return run;
}

function preferred() {
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function remember(lang) {
  try {
    window.localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // A browser that stores nothing still shows the tabs; it only forgets the choice.
  }
}

function select(bar, lang) {
  bar.dataset.selected = lang;
  bar.querySelectorAll('.code-tabs__tab').forEach((tab) => {
    const on = tab.dataset.lang === lang;
    tab.setAttribute('aria-selected', String(on));
    tab.tabIndex = on ? 0 : -1;
  });
  runFrom(bar.nextElementSibling).forEach((block) => {
    const hide = language(block) !== lang;
    if (block.hidden !== hide) block.hidden = hide;
  });
}

function reapply() {
  document.querySelectorAll('.code-tabs__bar').forEach((bar) => select(bar, bar.dataset.selected));
}

function makeBar(langs) {
  const bar = document.createElement('div');
  bar.className = 'code-tabs__bar';
  bar.setAttribute('role', 'tablist');
  langs.forEach((lang) => {
    const tab = document.createElement('button');
    tab.type = 'button';
    tab.className = 'code-tabs__tab';
    tab.dataset.lang = lang;
    tab.setAttribute('role', 'tab');
    tab.textContent = LABELS[lang];
    tab.addEventListener('click', () => {
      remember(lang);
      document.querySelectorAll('.code-tabs__bar').forEach((other) => {
        if (other.querySelector(`[data-lang="${lang}"]`)) select(other, lang);
      });
    });
    bar.appendChild(tab);
  });
  return bar;
}

function build() {
  document.querySelectorAll('.code-tabs__bar').forEach((bar) => bar.remove());
  document.querySelectorAll('.theme-code-block[hidden]').forEach((block) => {
    block.hidden = false;
  });
  const want = preferred();
  document.querySelectorAll('.markdown .theme-code-block').forEach((first) => {
    if (isCodeBlock(first.previousElementSibling)) return;
    const run = runFrom(first);
    const langs = run.map(language);
    const alternatives = langs.every((l) => l && LABELS[l]) && new Set(langs).size === run.length;
    if (run.length < 2 || !alternatives) return;
    const bar = makeBar(langs);
    first.parentNode.insertBefore(bar, first);
    select(bar, langs.includes(want) ? want : langs[0]);
  });

  // Keep each bar's choice when the blocks under it are re-rendered.
  if (watcher) watcher.disconnect();
  const root = document.querySelector('.markdown');
  if (!root || typeof MutationObserver === 'undefined') return;
  let pending = false;
  watcher = new MutationObserver(() => {
    if (pending) return;
    pending = true;
    window.setTimeout(() => {
      pending = false;
      reapply();
    }, 50);
  });
  watcher.observe(root, {childList: true, subtree: true});
}

// Timers rather than animation frames: a browser pauses animation frames while a page is not visible,
// and a page opened in a background tab would then show every language at once until it was looked at.
export function onRouteDidUpdate() {
  // After the route's content has rendered, not during it.
  window.setTimeout(build, 0);
}
