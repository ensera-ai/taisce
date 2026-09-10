<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Embedding adapter limits

The adapter accepts 1–64 nonblank UTF-8 texts per call, at most 64 KiB per text and 1 MiB total.
Split larger jobs into batches before calling it. An empty input list returns no vectors without a
network call when an embedding model is configured. An unconfigured embedding request refuses.

Responses are limited to 64 MiB. Every input needs exactly one indexed vector, and every vector in
a batch needs the same dimension. Vectors must contain 1–16,000 finite components and have nonzero
norm. Invalid input or output refuses the batch; callers never receive a partial result. Diagnostics
contain limits and positions, not source text or provider bodies. The cosine helper applies the same
numeric validity rules and returns a finite score in [-1,1] for comparable vectors.

These are defensive resource limits (D103), not the provider's token window or measured capacity.
The provider may impose tighter limits. They do not establish the storage contract: persistent model
identity, dimension compatibility, index support and model-change cutover remain #101/#19.
