<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Embedding adapter limits

Taisce talks to an embedding model through one small adapter. The adapter checks everything it sends
and everything it gets back, so a misbehaving provider cannot push bad or oversized data into
storage. This page lists those checks.

These are safety limits on resources. They are not your provider's token window and not a measure of
capacity. Your provider may set tighter limits of its own.

## What you can send

| Limit | Value |
|---|---|
| Texts per call | 1 to 64 |
| Size of one text | up to 64 KiB, valid UTF-8, not blank |
| Size of one call | up to 1 MiB in total |

Split bigger jobs into batches before you call it.

- An empty list returns no vectors and makes no network call, as long as an embedding model is
  configured.
- If no embedding model is configured, any request is refused.

## What the provider must send back

| Check | Rule |
|---|---|
| Response size | up to 64 MiB |
| Vectors | exactly one per input, by index |
| Dimensions | the same for every vector in a batch, 1 to 16,000 |
| Values | every component finite, and the vector not all zeros |

If any input or output breaks a rule, the whole batch is refused. You never get part of a result.
Error messages name the limit and the position that broke it. They never include your text or the
provider's response body.

## Comparing two vectors

The cosine helper applies the same checks to its inputs and returns a finite score between -1 and 1
for vectors it can compare.

## Where the rest lives

The adapter does not decide how vectors are stored. Model identity, checking that dimensions match,
index support and switching models are handled by embedding generations. See
[Message embedding generations](22-message-embeddings.md) and
[Community-report embeddings](25-report-embeddings.md).

The limits are defined in [`embedder.go`](../internal/infra/inference/embedder.go).
