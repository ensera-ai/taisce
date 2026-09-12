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
    """The text of the user entry that started this turn, or "". Reads from the end: a long
    session's transcript is large and the turn that just ended is on its last lines.

    A turn that used a tool ends with the tools' results, and the transcript records each one as a
    user-type entry holding only tool_result blocks. Those are the tools' words, not the person's,
    so they are passed over, as are the meta entries Claude Code adds, until an entry with text."""
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
        if entry.get("type") != "user" or entry.get("isMeta"):
            continue
        content = (entry.get("message") or {}).get("content")
        if isinstance(content, str):
            text = content
        elif isinstance(content, list):
            text = "\n".join(c.get("text", "") for c in content
                             if isinstance(c, dict) and c.get("type") == "text" and c.get("text"))
        else:
            text = ""
        if text.strip():
            return text
    return ""


def main() -> int:
    if not taisce.configured() or not taisce.AUTO_CAPTURE:
        return 0
    event = taisce.hook_input()
    text = last_user_message(event.get("transcript_path") or "").strip()
    # Nothing said; a command rather than a message; or markup Claude Code wrote, such as a
    # command's expansion or a background task's notice, which no person said. A message that
    # genuinely begins with "<" is skipped with them: a missed turn can be recorded with the
    # remember command, and a notice recorded as a person's words cannot be taken back quietly.
    if not text or text.startswith(("/", "<")):
        return 0
    digest = hashlib.sha256((event.get("session_id", "") + "\n" + text).encode()).digest()
    key = str(uuid.UUID(bytes=digest[:16], version=5))
    body = {"idempotency_key": key, "messages": [{"role": "user", "content": text}]}
    if taisce.DATA_SUBJECT_ID:
        body["data_subject_id"] = taisce.DATA_SUBJECT_ID
    _, failure = taisce.request("POST", "/v1/observations", body)
    if failure:
        # A write failure is never silent. It is reported to the person as a message and the hook
        # exits 0: exiting 2 would keep the session from stopping, and a memory that failed to save
        # is not a reason to hold the conversation open.
        print(json.dumps({"systemMessage": "Taisce did not record this turn: " + failure + "."}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
