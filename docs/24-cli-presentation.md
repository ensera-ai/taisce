<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Terminal presentation

Taisce keeps stdout machine-readable whenever it is redirected. On a terminal, help, operator health,
project rebuild status and command failures use the compact branded view. Long project rebuild and fact
recovery operations show an indeterminate elapsed-time indicator on stderr; the final result remains on
stdout. The redraw runs at ten frames per second and clears its line on success, failure or Ctrl-C.

`TAISCE_OUTPUT=json` forces the established JSON/plain output. `TAISCE_OUTPUT=fancy` forces the terminal
view for previews and recordings. `NO_COLOR` removes color, `TAISCE_ASCII=1` replaces Unicode graphics,
and `COLUMNS` supplies a width from 40 through 240 when the environment knows the terminal width. A
`dumb` terminal always takes the non-decorative path unless fancy output was explicitly requested.

Dynamic labels pass through control-character removal and display-width clipping before rendering.
The panels contain aggregate health and operation metadata only. They never include credentials,
source memory, provider responses or raw database/provider errors. Script exit status and JSON field
names are unchanged.

The visual vocabulary is deliberately small: amber names Taisce, blue marks navigation, green/red
describe health, and borders group one operational snapshot. A wide terminal uses Unicode rules; a
narrow or ASCII terminal keeps the same reading order:

```text
  TAISCE  instance healthy
  + memory path -----------------------------------+
  | formation    responsive                        |
  | backlog      ###............... 2/10            |
  | PostgreSQL   2 instance · 3 cluster · 100 max  |
  | inference    configured                        |
  +------------------------------------------------+
```

The status view does not invent a project total or model-health check. Project names are absent from
the aggregate health query, and “configured” means an embedding model is configured, not that an
inference request succeeded.
