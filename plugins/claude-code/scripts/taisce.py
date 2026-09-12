# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""The one place the hooks talk to a deployment.

Standard library only. A hook runs in whatever Python the machine has, and a dependency would turn
"install the plugin" into "manage an environment"; a hook that fails to import breaks a session it
was meant to help. The hooks speak the v1 contract directly rather than MCP, because a hook is a
script with a stdin and a stdout, not a model with a tool list.

Nothing here raises. A hook that throws interrupts the session, and no memory feature is worth
that: an unreachable deployment degrades to a session without memory, never to one that will not
start. The caller decides whether silence is worth reporting.
"""
from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any, Dict, Optional, Tuple

# Plugin settings arrive as CLAUDE_PLUGIN_OPTION_<KEY>, uppercased from plugin.json's userConfig.
ENDPOINT = (os.environ.get("CLAUDE_PLUGIN_OPTION_ENDPOINT") or "").rstrip("/")
TOKEN = os.environ.get("CLAUDE_PLUGIN_OPTION_TOKEN") or ""
DATA_SUBJECT_ID = os.environ.get("CLAUDE_PLUGIN_OPTION_DATA_SUBJECT_ID") or ""
AUTO_CAPTURE = (os.environ.get("CLAUDE_PLUGIN_OPTION_AUTO_CAPTURE") or "").lower() in ("1", "true", "yes")
TIMEOUT = float(os.environ.get("TAISCE_PLUGIN_TIMEOUT", "8"))


def configured() -> bool:
    return bool(ENDPOINT and TOKEN)


def request(method: str, path: str, body: Optional[Dict[str, Any]] = None) -> Tuple[Optional[dict], str]:
    """One request against the v1 contract: the decoded answer and "", or None and why it failed.

    The reason names what went wrong and never the credential or the body, because it is shown to
    the person and may be copied into an issue."""
    if not configured():
        return None, "the plugin is not configured"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        ENDPOINT + path, data=data, method=method,
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + TOKEN},
    )
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
            raw = r.read()
        return json.loads(raw or b"{}"), ""
    except urllib.error.HTTPError as e:
        code = ""
        try:
            code = json.loads(e.read() or b"{}").get("error", {}).get("code", "")
        except (ValueError, OSError, AttributeError):
            pass
        return None, "the deployment answered {}{}".format(e.code, " " + code if code else "")
    except (urllib.error.URLError, OSError, TimeoutError):
        return None, "the deployment could not be reached"
    except ValueError:
        return None, "the deployment's answer was not JSON"


def call(method: str, path: str, body: Optional[Dict[str, Any]] = None) -> Optional[dict]:
    """One request against the v1 contract, decoded, or None on any failure."""
    return request(method, path, body)[0]


def hook_input() -> dict:
    """The hook event on stdin, or an empty dict."""
    try:
        raw = sys.stdin.read()
        return json.loads(raw) if raw.strip() else {}
    except (ValueError, OSError):
        return {}
