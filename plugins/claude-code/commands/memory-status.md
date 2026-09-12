---
description: How far behind is the memory, and is anything parked?
---

Report the state of Taisce memory.

Call the `freshness` tool of the `taisce` MCP server. It returns `stored`, the highest offset the
deployment holds; `formed`, the highest offset it has formed into facts, absent until the first turn
forms; and `parked`, how many turns the formation worker gave up on.

Present it in plain language: how many turns are held, whether forming has caught up, and how many
are parked. **If `formed` is behind `stored`, say so plainly**: a fact asked for right now may not
include the last turns, and a caller who does not know that reads an absence as a negative. A parked
turn will not form until an operator retries it with `taisce formation unpark`; say that too.
