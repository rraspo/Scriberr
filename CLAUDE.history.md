# CLAUDE.history.md — Scriberr fork

- 2026-11-10 — All fork branches (remote WhisperX, job source field, GPU power buttons, copy transcript, deploy docs) folded into `master`; `master` is now what the live container runs and work commits there directly.
- 2026-11-10 — Deploy section rewritten as ship-on-done: the spare-port rehearsal and the per-recreate owner go are gone (owner: a local tool must not ask for deploys); a DB backup still precedes every recreate.
- 2026-10-26 — The deploy contract moved into the `## Deploy` section of CLAUDE.md, assembled from the cut-over ritual on the deploy-fork-image card (SCRI-10, reused verbatim by later deploy cards) and the live service's vault note.
