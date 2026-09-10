#!/usr/bin/env python3
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
"""Stop: record the user's message of the turn that just completed, when asked to.

OFF unless auto_capture is set, and that default is the honest one: turning it on sends what the
user typed in every completed turn to the deployment, and a setting that does that should be one
the person turned on. Only the user's message is sent, as a user-role message, because a memory is
evidence of what a person said; the assistant's words are the assistant's and are not recorded
here. The idempotency key is derived from the session and the message, so a hook that fires twice
for one turn records once.
"""
import hashlib
import json
import sys
import uuid

import taisce


def last_user_message(transcript_path: str) -> str:
    """The last user message in the transcript, or "". Reads from the end: a long session's
    transcript is large and the turn that just ended is on its last lines."""
    if not transcript_path:
        return ""
    try:
        with open(transcript_path, "r", encoding="utf-8") as f:
            lines = f.readlines()
    except OSError:
        return ""
    for line in reversed(lines):
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        if entry.get("type") != "user":
            continue
        message = entry.get("message") or {}
        content = message.get("content")
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            texts = [c.get("text", "") for c in content if isinstance(c, dict) and c.get("type") == "text"]
            return "\n".join(t for t in texts if t)
    return ""


def main() -> int:
    if not taisce.configured() or not taisce.AUTO_CAPTURE:
        return 0
    event = taisce.hook_input()
    text = last_user_message(event.get("transcript_path") or "").strip()
    if not text or text.startswith("/"):
        return 0  # nothing said, or a command rather than a message
    digest = hashlib.sha256((event.get("session_id", "") + "\n" + text).encode()).digest()
    key = str(uuid.UUID(bytes=digest[:16], version=5))
    body = {"idempotency_key": key, "messages": [{"role": "user", "content": text}]}
    if taisce.DATA_SUBJECT_ID:
        body["data_subject_id"] = taisce.DATA_SUBJECT_ID
    taisce.call("POST", "/v1/observations", body)
    return 0


if __name__ == "__main__":
    sys.exit(main())
