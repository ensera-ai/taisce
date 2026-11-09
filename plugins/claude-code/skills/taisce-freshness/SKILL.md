---
name: taisce-freshness
description: Check how far behind memory is before trusting an absence, after recording something, or when the user asks whether memory is current or whether anything is stuck. Calls the freshness tool on the taisce MCP server. Reads only.
---

# How far behind memory is

`freshness` is the tool. It returns the highest offset **stored**, the highest **formed**, and how
many turns are **parked**.

## Why this matters more than it looks

Storing a turn and being able to recall it are separated by a model call that happens behind the
write. So there is always a window in which something was said, was kept, and cannot yet be found.
Three numbers describe where you are in it:

- **stored − formed** is the backlog. Anything in it is in memory and not yet answerable.
- **parked** is the one to read carefully. A parked turn failed formation enough times that the
  deployment moved past it. It is not retried on its own, so it will not appear in a recall later
  either — it is held, and it is not findable. A non-zero parked count is something to tell the user
  about, not something to work around.

## When to call it

- Before concluding that memory does not contain something. An empty recall with a large backlog
  means "not yet", and reporting that as "nothing recorded" is a false answer.
- After recording something the user expects to be able to ask about.
- When the user asks whether memory is up to date, or why something they just said cannot be found.

## What it does not tell you

Whether the model is working. A backlog that is not shrinking and a backlog that is being worked
through look the same in one reading — two readings apart tell you which. And it says nothing about
*quality*: a formed turn is a turn the deployment extracted facts from, not a turn it understood.
