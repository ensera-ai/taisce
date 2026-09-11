---
description: Record something worth keeping in Taisce memory, in the user's own words.
argument-hint: <what to remember>
---

Record this in Taisce memory: $ARGUMENTS

Call the `observe` tool of the `taisce` MCP server with:

- `idempotency_key`: a fresh UUID for this command. Running the same command twice with the same
  key records once; a new key is a new record.
- `messages`: one message, `role` `user`, `content` set to what the user asked to remember, in their
  words. Do not summarise it into your own: a memory is evidence of what was said, and a paraphrase
  is evidence of what you understood.
- `data_subject_id`: the plugin's configured data subject, if there is one.

Then say, in one line, what was recorded. Do not repeat the content back in full. Formation runs
afterwards; the `freshness` tool says when it has caught up, and until then a recall may not include
what was just recorded.

**Do not record secrets.** Keys, tokens, passwords and personal data of third parties are not
memories, and the deployment keeps what it is given. If the user asks to remember something about a
person, record it under that person's data subject only if the plugin is configured for them;
otherwise say that it would be recorded project-wide, reachable by no subject erasure and only by
the observation id the record returns.
