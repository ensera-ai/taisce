---
name: taisce-observe
description: Record what was said, in the speaker's own words, when a decision is reached, a constraint is discovered the hard way, or the user corrects your understanding. Calls the observe tool on the taisce MCP server. This one writes.
---

# Record the words, not your summary of them

`observe` is the tool. It takes messages with the role of whoever said each, and an
`idempotency_key`, and it **writes**.

## Record at the moment, not at the end

- A choice with a reason attached: "we are using X because Y". The reason is the memory; the choice
  alone is a fact anybody can read off the code later.
- A constraint found the hard way: "Z fails on this hardware, and here is what it does".
- A correction the user makes to your understanding. This is the highest-value memory there is,
  because it is the one nothing else in the repository will ever contain.

## How

The user's own words, in a `user`-role message, with a fresh UUID as `idempotency_key`. A key that
repeats stores once — that is what makes a retry safe, and what makes a hook that fires twice
harmless. Do not rewrite what was said into your own phrasing: the evidence behind a fact is the
point of recording it, and a summary is evidence of you.

## What must not be recorded

**Secrets.** Keys, tokens, passwords. The deployment keeps what it is given, and a token in a
transcript is not a memory.

**Other people's personal data**, unless the person whose memory this is said it. A subject access
request answered from this project returns what is attributed to that subject; a third party's
details recorded under somebody else's name are both wrong and unreachable by the person they are
about.

**Your own reasoning.** A model's summary entering memory as though somebody said it is how a corpus
starts believing itself. Record the human sentence.

## It is asynchronous

A stored turn is not a recallable fact yet — forming it is a model call that happens behind the
write. `taisce-freshness` says how far behind. Do not record something and then recall it in the same
breath and conclude memory is broken.
