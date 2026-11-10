# CLAUDE.history.md — Scriberr fork

- 2026-11-10 — Added a second GPU tier (the fallback profile's own remote host) between the primary GPU host and local CPU, Ollama endpoint failover via `OLLAMA_ENDPOINTS`, per-request `num_ctx` (Ollama had been truncating long transcripts at its default window), automatic summaries after each completed job, and a real LLM-ready check in place of a hardcoded `true`. Models chosen by a benchmark on an 8 GB shared card: large-v3-turbo int8 for transcription, qwen3:4b-instruct-2507 q4_K_M for summaries.

- 2026-11-10 — All fork branches (remote WhisperX, job source field, GPU power buttons, copy transcript, deploy docs) folded into `master`; `master` is now what the live container runs and work commits there directly.
- 2026-11-10 — Deploy section rewritten as ship-on-done: the spare-port rehearsal and the per-recreate owner go are gone (owner: a local tool must not ask for deploys); a DB backup still precedes every recreate.
- 2026-10-26 — The deploy contract moved into the `## Deploy` section of CLAUDE.md, assembled from the cut-over ritual on the deploy-fork-image card (SCRI-10, reused verbatim by later deploy cards) and the live service's vault note.
