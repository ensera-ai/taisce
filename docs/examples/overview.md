<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Examples

Each recipe covers one task from start to finish: the goal, the calls to make, what you should see,
and where to go next. They are short, and you can run them in any order.

| Recipe | What you will do |
|---|---|
| [Remember and recall](remember-and-recall.md) | Save a user's preferences and get back that user's memories, and nobody else's |
| [Answer with evidence](answer-with-evidence.md) | Show the exact quote behind each answer, and trace a claim back to its source |
| [Forget a person](forget-a-person.md) | Erase everything one person said, and read the receipt that counts what is left |
| [Long conversations](long-conversations.md) | Keep a long history small enough for a prompt, with the older part already summarised |
| [Memory for a coding agent](coding-agent-memory.md) | Give Claude Code memory that lasts across sessions, over MCP |

## Before you start

Every recipe assumes Taisce is running on your machine and you have a project token. The
[quickstart](../developers/quickstart.md) sets up both. Then set two environment variables:

```bash
export TAISCE_URL=http://localhost:8080
export TOKEN=tsk_…   # the project token from the quickstart
```

Each HTTP call is shown with curl and with Python. The curl versions use `jq` to pull fields out of
answers, and `uuidgen` for idempotency keys. On Linux without `uuidgen`, use
`cat /proc/sys/kernel/random/uuid` instead.

The Python versions use [httpx](https://www.python-httpx.org) (`pip install httpx`). The Python
blocks in a recipe continue one script, which starts like this:

```python
import os
import time
import uuid

import httpx

client = httpx.Client(
    base_url=os.environ["TAISCE_URL"],
    headers={"Authorization": f"Bearer {os.environ['TOKEN']}"},
    timeout=35.0,  # a little longer than the server's own 30-second request limit
)


def wait_formed(offset: int) -> None:
    """Wait until the background worker has turned everything up to `offset` into facts."""
    while True:
        f = client.get("/v1/freshness").raise_for_status().json()
        if f["formed"] is not None and f["formed"] >= offset:
            return
        time.sleep(2)
```

`wait_formed` uses the two numbers explained in [how it works](../start/how-it-works.md): a turn is
in memory once `formed` reaches the `log_offset` its write returned.

> [!NOTE]
> The model writes the fact statements, so their wording varies from run to run and may differ a
> little from what is shown here. Ids, offsets and timestamps are shown as `…`.
