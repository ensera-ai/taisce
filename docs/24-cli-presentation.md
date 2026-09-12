<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Terminal presentation

The `taisce` command shows a compact, coloured view when you run it in a terminal, and plain JSON
when its output goes to a pipe or a file. Scripts keep working unchanged.

## What you see in a terminal

Help, `taisce health`, project rebuild status and error messages use the terminal view. Long project
rebuilds and fact recovery show an elapsed-time spinner on stderr while they run. The final result
still goes to stdout. The spinner redraws ten times a second and clears its line when the command
succeeds, fails or you press Ctrl-C.

Here is `taisce health` on a narrow or ASCII terminal. A wide terminal draws the same thing with
Unicode lines:

```text
  TAISCE  instance healthy
  + memory path -----------------------------------+
  | formation    responsive                        |
  | backlog      ###............... 2/10            |
  | PostgreSQL   2 instance · 3 cluster · 100 max  |
  | inference    configured                        |
  +------------------------------------------------+
```

- **PostgreSQL** shows connections to this database, connections to the whole server, and
  `max_connections`.
- **inference: configured** means an embedding model is configured. It does not mean a model call
  succeeded.
- There is no project count. The health query does not look at project names.

Colours are few on purpose: amber for the Taisce name, blue for navigation, green and red for health.
A border groups one snapshot.

## Changing the output

| Variable | Effect |
|---|---|
| `TAISCE_OUTPUT=json` | Always print JSON or plain text |
| `TAISCE_OUTPUT=fancy` | Always print the terminal view, even when piped (useful for recordings) |
| `NO_COLOR` | Remove colour |
| `TAISCE_ASCII=1` | Replace Unicode graphics with ASCII |
| `COLUMNS` | Set the width, from 40 to 240 |

A terminal with `TERM=dumb` always gets the plain view, unless you ask for `TAISCE_OUTPUT=fancy`.

Exit codes and JSON field names are the same in every mode.

## What it never shows

Before anything is drawn, labels are stripped of control characters and clipped to the display
width. The panels only show totals and operation details. They never show credentials, stored
memory, model responses, or raw database or provider errors.
