# CLAUDE.history.md — Scriberr fork

- 2026-11-28 — Instagram image posts failed with `download_failed` because yt-dlp only handles video. They are now stored as completed "post" jobs with their images as frames, read from the post page yt-dlp fetches.

- 2026-11-28 — A 61 s reel got no visuals: its container ran 20 ms past the video stream, ffmpeg produced 61 grid frames where 62 were predicted, and the build failed on the missing last one. The build now keeps the frames that were produced. The merged-stream files `-k` left beside the video are removed too.

- 2026-11-28 — Link imports keep their video and build timestamped frames and contact sheets over the whole video. Owner ask: sessions cannot watch video, and visual reels (Instagram, TikTok) carry content that is not in the audio, so a session reads the frames instead.

- 2026-11-28 — Link imports with no reported language now default to English. They had defaulted to Spanish, and the owner's English reels were transcribed in Spanish. The egress proxy covers only Instagram and TikTok: mission-control kept YouTube off it. The owner saw "YouTube" on an Instagram recording, most likely a cached app shell from the previous build, so the shell is now served no-cache and the page has an explicit "Watch on <source>" link.

- 2026-11-28 — The link import takes Instagram reels and posts and TikTok videos as well as YouTube, and keeps each post's caption. Buzon gets its own idempotent import and status API, and the list gains per-source icons and a source filter. Owner ask: links he shares from his phone become reference material. X stays unsupported because it doesn't download anonymously.

- 2026-11-27 — Three WhatsApp recordings sat for 45 minutes: the primary GPU host was asleep, and the fallback GPU host's wrapper waited forever for room on a card another voice service held. Wrappers now give up after their wait limit with exit 5, and Scriberr moves on to the next tier. A cancelled run now kills its wrapper on the host; before, the orphan kept the card's lock.

- 2026-11-26 — WhatsApp rows dropped the "Voice note"/"Audio" split, which was guessed from the .ogg extension. Owner: no sender/chat capture, since he forwards audios to Voicenotes. Video uploads now get `source=video`; older ones stay `manual`.

- 2026-11-26 — YouTube jobs no longer use the default profile ("4090 Spanish" transcribed an English video into Spanish). A paste preview now shows the video's metadata and pre-selects the profile from YouTube's language. Auto-detect was rejected because it had misread an English talk as Welsh. Thumbnails are saved with the job and shown in the list.

- 2026-11-26 — YouTube links now queue immediately with source `youtube`, keep the source URL and channel for traceability (guide videos), and return classified errors. Before this, they landed `uploaded` with source `manual`, were never queued, and surfaced raw yt-dlp errors. Owner ask relayed from the vault session.

- 2026-11-16 — Stuck-job fixes after a 25-minute recording sat in pending: the automatic summary moved off the single queue worker (argus Ollama had lost its GPU and took 5m45s on CPU, blocking every queued job); a restart during a kill now answers 409 instead of being silently overwritten; remote attempts get unique run directories (a cancelled run's cleanup deleted the next run's input on argus); the CPU tier uses int8 / batch 4 for every local run (float32 with GPU batch sizes was OOM-killed, exit 137); tiers are recorded as they start and CPU runs show a "Slow path" badge.

- 2026-11-13 — `scriberr watch` gained `--source`; the WhatsApp inbox watcher now marks its jobs `source=whatsapp` (they had been `manual`), so the meetings import can skip client voice notes without matching titles.

- 2026-11-10 — Added a second GPU tier (the fallback profile's own remote host) between the primary GPU host and local CPU, Ollama endpoint failover via `OLLAMA_ENDPOINTS`, per-request `num_ctx` (Ollama had been truncating long transcripts at its default window), automatic summaries after each completed job, and a real LLM-ready check in place of a hardcoded `true`. Models chosen by a benchmark on an 8 GB shared card: large-v3-turbo int8 for transcription, qwen3:4b-instruct-2507 q4_K_M for summaries.

- 2026-11-10 — All fork branches (remote WhisperX, job source field, GPU power buttons, copy transcript, deploy docs) folded into `master`; `master` is now what the live container runs and work commits there directly.
- 2026-11-10 — Deploy section rewritten as ship-on-done: the spare-port rehearsal and the per-recreate owner go are gone (owner: a local tool must not ask for deploys); a DB backup still precedes every recreate.
- 2026-10-26 — The deploy contract moved into the `## Deploy` section of CLAUDE.md, assembled from the cut-over ritual on the deploy-fork-image card (SCRI-10, reused verbatim by later deploy cards) and the live service's vault note.
