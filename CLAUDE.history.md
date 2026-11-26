# CLAUDE.history.md — Scriberr fork

- 2026-11-26 — YouTube links now queue immediately with source `youtube`, keep the source URL and channel for traceability (guide videos), and return classified errors. Before this, they landed `uploaded` with source `manual`, were never queued, and surfaced raw yt-dlp errors. Owner ask relayed from the vault session.

- 2026-11-16 — Stuck-job fixes after a 25-minute recording sat in pending: the automatic summary moved off the single queue worker (argus Ollama had lost its GPU and took 5m45s on CPU, blocking every queued job); a restart during a kill now answers 409 instead of being silently overwritten; remote attempts get unique run directories (a cancelled run's cleanup deleted the next run's input on argus); the CPU tier uses int8 / batch 4 for every local run (float32 with GPU batch sizes was OOM-killed, exit 137); tiers are recorded as they start and CPU runs show a "Slow path" badge.

- 2026-11-13 — `scriberr watch` gained `--source`; the WhatsApp inbox watcher now marks its jobs `source=whatsapp` (they had been `manual`), so the meetings import can skip client voice notes without matching titles.

- 2026-11-10 — Added a second GPU tier (the fallback profile's own remote host) between the primary GPU host and local CPU, Ollama endpoint failover via `OLLAMA_ENDPOINTS`, per-request `num_ctx` (Ollama had been truncating long transcripts at its default window), automatic summaries after each completed job, and a real LLM-ready check in place of a hardcoded `true`. Models chosen by a benchmark on an 8 GB shared card: large-v3-turbo int8 for transcription, qwen3:4b-instruct-2507 q4_K_M for summaries.

- 2026-11-10 — All fork branches (remote WhisperX, job source field, GPU power buttons, copy transcript, deploy docs) folded into `master`; `master` is now what the live container runs and work commits there directly.
- 2026-11-10 — Deploy section rewritten as ship-on-done: the spare-port rehearsal and the per-recreate owner go are gone (owner: a local tool must not ask for deploys); a DB backup still precedes every recreate.
- 2026-10-26 — The deploy contract moved into the `## Deploy` section of CLAUDE.md, assembled from the cut-over ritual on the deploy-fork-image card (SCRI-10, reused verbatim by later deploy cards) and the live service's vault note.
