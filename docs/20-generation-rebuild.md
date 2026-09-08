<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Fact generation and rebuild

Formation writes facts from a stored turn under a pinned extraction identity. When the model,
prompt, admission rules or vocabulary change, an operator can reinterpret one retained turn with:

```text
taisce rebuild facts --project <project> --source <observation-uuid> --key <operation-uuid>
```

The source must be an ordinary conversation turn, not curated or authored input. The operation reads
at most 256 messages and 1 MiB, performs model work before taking the cutover lock, and then checks
that the source and its fact revision are unchanged. Publication retires only model-derived facts
from that source and records a new generation with bounded per-fact dispositions. A human correction
is retained and blocks generated claims for the same message subject/relation slot. A source-backed
withdrawal continues to block its matching structural claim.

Missing current fact projections are restored from their surviving receipts inside cutover, then
retired along with retained facts. A damaged receipt refuses publication; later receipt recovery
cannot revive the replaced interpretation. This restoration has the same 100-fact bound.

The previous interpretation remains available as historical knowledge while the new one is current.
Citation responses expose the generation that admitted a fact and the generation that retired it;
exports and subject erasure include the same lineage. The JSON result reports counts and contains no
message text or provider diagnostics.

`--key` is an operator-owned idempotency key. Repeating it with the same source and extractor
identity returns the committed result and finishes the formed watermark without another model call.
A different identity for an already committed key, a changed source, a missing source or a failed
model attempt is refused without partial publication. A scope-wide rebuild and model-response cache
are separate roadmap items and are not implied by this command.
