<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Extraction models, thinking, and a rented GPU — 2026-09-11

Extraction is the one step in Taisce that needs a language model on the write path. The model reads a
message and proposes relations from a closed list, each with an exact quote, and the extractor refuses
anything it cannot verify. This measurement asks which models hold that contract, what a model's
"thinking" does to it, and what it takes to serve the one that needs a rented GPU for a demo.

## What this means for you

- **The local default holds the contract.** `qwen3.6:35b-a3b-mxfp8` on Ollama passed all 19 corpus
  cases, three attempts each.
- **Qwen3.8-27B holds it too, with thinking off.** Served by vLLM on one rented RTX PRO 6000, it passed
  all 19. With thinking on under Ollama it did not answer at all: every call ran past the client's two
  minutes. The earlier note that it "returns JSON of the wrong shape" did not reproduce.
- **A hosted model holds it.** `deepseek-flash` passed all 19. Conversation text then goes to a third
  party, which the operator has to decide to allow. The `deepseek` inference profile sets it up.
- **Taisce does not control thinking.** The extraction request carries a temperature and a response
  format, and nothing about whether the model reasons first. Ollama thinks by default for both Qwen
  models, so the local default pays for a reasoning trace on every extraction.
- **For a demo on a rented card, expect about four seconds per extraction from a laptop.** Formation
  runs after the message is stored, so nobody waits on it.

## The corpus

`TestTheExtractionCorpus` in `internal/infra/inference/corpus_test.go` sends 19 messages through the
extractor three times each. Fourteen cases fail on any missing or wrong relation. Five are marked
pending because the default model has failed them intermittently, and each is a known gap that is
still open: the denial, the hedge and the possibility, reported speech, and the tool result that
instructs the extractor. A pending
case that behaves correctly in one run of three attempts is a good run, not evidence the gap is closed.
Every run used an `internal/infra/inference` identical to `e5700ad`.

## Results

| Model | Served by | Thinking | Corpus | Wall time, 57 calls |
|---|---|---|---|---|
| `qwen3.6:35b-a3b-mxfp8` | Ollama on the development laptop | On (server default) | 19/19, pending all clean | 1,644 s |
| `Qwen/Qwen3.8-27B` BF16, revision `1d4bf0f` | vLLM 0.29.0, one RTX PRO 6000, us-central | Off (server default set to off) | 19/19, pending all clean | 226 s |
| `deepseek-flash` | The provider's hosted API | Provider default (reasons) | 19/19, pending all clean | 87 s |
| `qwen3.8:27b-mxfp8` | Ollama on the development laptop | On | No result: every call exceeded the two-minute bound | — |
| `qwen3.8:27b-mxfp8` | Ollama on the development laptop | Off, through an uncommitted request patch | 8 of 8 cases passed before the run was paused | — |

The wall times describe three different arrangements — a laptop, a rented card, a hosted service — and
are setup timings, not a comparison of the models.

The three complete passes produced the same relations and the same refusal reasons on every case. The
only difference was quote length: one model quotes `have worked at Ensera`, another
`I have worked at Ensera since 2019.`, and both are exact spans of the message.

## Thinking

One short extraction-shaped request to each Qwen model on Ollama, with and without
`reasoning_effort: "none"`:

| Model | Thinking on | Thinking off |
|---|---|---|
| `qwen3.6:35b-a3b-mxfp8` | 14.0 s, 383 completion tokens | 0.6 s, 25 tokens |
| `qwen3.8:27b-mxfp8` | 15.8 s, 135 completion tokens (36.7 s on the first call) | 3.4 s, 25 tokens |

Both answers were correct either way. With thinking on, both replies also carried a fragment of text
before the JSON, which the extractor's parse tolerates. On the full extraction prompt, Qwen3.8's
reasoning did not finish inside two minutes on this laptop.

What this leaves open: whether the extraction contract should say that the model must not reason. The
default model's 19 of 19 was measured with thinking on, so switching it off is a change in behaviour
that needs its own corpus run, and that run has not been done.

## Latency through the demo tunnel

From the development laptop, through an SSH tunnel via the provider's relay to one RTX PRO 6000 in
us-central, vLLM 0.29.0, BF16, thinking off. The request is the committed extraction prompt golden
(about 1,284 prompt tokens) and a corpus sentence; the median completion was 90 tokens.

| Measurement | Result |
|---|---|
| Round trip, no model work (n=10) | 559 ms median, 569 ms maximum |
| Extraction request, sequential (n=20) | 4.02 s median, 9.35 s 95th percentile and maximum |
| Time to first token (n=5) | 674 ms median |
| Decode | about 26 tokens per second |

About half a second is the network path. Nearly all the rest is decoding: a 27B model in BF16 reads
about 55 GB of weights for every token, and at one request at a time the card's memory bandwidth sets
the pace. That is an inference from the model's size, not a measurement of the card. None of these
numbers is a performance claim for Taisce: they describe one laptop, one network path and one rented
card.

## Serving the model on rented capacity

What failed, in the order it happened, and what `deploy/gpu/controller/demo.py` does about it now:

1. **EU capacity moved within the hour.** A single A100 in the EU was listed and then gone. EU single-card
   offerings were container-only. The demo filters by region family and refuses rather than placing
   elsewhere.
2. **Container capacity refuses an image that defines an ENTRYPOINT.** The qualified vLLM image does,
   and clearing it in the launch request was refused the same way, so the check reads the image. The
   demo launches the provider's base container and installs a pinned vLLM over SSH instead.
3. **The base container has no Python headers.** Triton compiles a helper against `Python.h` when vLLM
   starts, and the server died with `Model architectures ['Qwen3_5ForConditionalGeneration'] failed to
   be inspected`. The demo installs them when they are missing.
4. **FlashInfer's sampler could not compile.** On a node without a CUDA compiler it found no target
   architecture and warm-up died with `FlashInfer requires GPUs with sm75 or higher` on a compute
   capability 12.0 card. The demo sets `VLLM_USE_FLASHINFER_SAMPLER=0`; extraction decodes greedily, so
   the fallback sampler changes nothing a request can observe.
5. **The driver has to match the release.** vLLM 0.29.0 pins torch 2.13.0, built for CUDA 13; the node's
   driver 580.126.09 served it. The demo reads the driver's CUDA version and refuses below 13.0.

With those in place the node downloaded 52 GB of weights in about a minute and a half, loaded 51.1 GiB
onto the card and reported healthy about two and a half minutes after the start that found the weights
already on disk. `demo.py` now runs these steps; the run above performed them by hand on the node, and
the script has not yet been run end to end.

## What this does not establish

- Nineteen cases are a contract fixture, not a quality distribution, and each model ran once.
- Nothing here is a throughput or production latency figure.
- The default model has not been run through the corpus with thinking off, and the demo's Qwen3.6
  variant, which serves that family at full precision with thinking off, has not been run at all.
- The rented card was an RTX PRO 6000 and the server a PyPI vLLM release, not the A100 and the
  qualified image `deploy/gpu` measured.

## Reproduce

```sh
make test-inference                                            # the local default
make test-inference INFERENCE_PROFILE=deepseek                 # needs TAISCE_INFERENCE_API_KEY or DEEPSEEK_API_KEY
python3 deploy/gpu/controller/demo.py up --workdir ~/.taisce-demo --model 3.8 --region-prefix us
export TAISCE_INFERENCE_API_KEY="$(cat ~/.taisce-demo/api_key)"
make test-inference INFERENCE_PROFILE=demo-qwen3.8
python3 deploy/gpu/controller/demo.py down --workdir ~/.taisce-demo
```

`--model 3.6` with `INFERENCE_PROFILE=demo-qwen3.6` serves Qwen3.6-35B-A3B on the same path. It has not
been run; its result belongs here when it has.
