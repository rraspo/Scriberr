# Scriberr Fork Guide

## Purpose

This public fork adds parameterized remote WhisperX execution. Each profile can
select `local` or `remote` execution. Remote execution uses SSH and SFTP to
stage work on a user-configured GPU host, with automatic fallback to local
execution when the remote path is unavailable.

## Public repository hygiene

This is a public repository. Its tracked tree must never contain personal or
homelab identifiers, real private-network addresses, personal directory names,
or email addresses, including in tests, fixtures, documentation, and
placeholder copy. Use generic stand-ins such as `gpu-host.example`,
`/srv/scriberr-work`, and `10.0.0.1` only.

All remote connection parameters are configuration values. No host, address,
username, path, credential, or other connection detail may be hardcoded in the
repository.

## Branch policy

The default branch is `master` (owner decision 2026-08-04, overriding the
fork-keeps-upstream-default convention for this repo). Upstream tracking is
unaffected: pull upstream changes by rebasing or merging `upstream/main`.

`master` is exactly what the live container runs. Work commits straight to
`master` and the live image is always built from `master`; a branch or
worktree exists only for the length of a parallel task and is merged and
deleted before its change ships.

## Build baseline

The root `Makefile` defines the complete native application build:

```sh
make build
```

That target builds the two native halves with these underlying commands:

```sh
cd web/frontend && npm ci
cd web/frontend && npm run build
go build -o scriberr cmd/server/main.go
```

The frontend requires Node.js and npm. The backend requires the Go toolchain
declared by `go.mod` (Go 1.24 with the Go 1.24.4 toolchain). Do not install a
missing host toolchain as part of baseline verification.

The orchestrator verifies the standard container build from the repository
root with:

```sh
docker build -f Dockerfile .
```

Do not substitute a local Docker build when the Docker socket is unavailable.

## Deploy

- **Targets:** one container on the owner's local fleet, the owner's own transcription tool,
  running a locally built `scriberr-fork:<branch>-<short-sha>` image from a compose stack. It
  holds the owner's real transcripts and profiles. No public or hosted target; the repo
  publishes no image or release.
- **When:** shipping is part of finishing. A change is done when it runs in the local
  container, so a session builds and recreates in the same turn it verifies the change,
  without asking. Build from `master`, tagged `scriberr-fork:master-<short-sha>`.
- **Path:** local fleet, direct (no Mission Control runbook): build the image with an explicit
  traceable tag, back up the DB, edit only the `image:` lines of the stack's compose file,
  recreate. The folder-watcher sidecar is pinned to the identical tag so fork and watcher
  never skew.
- **Hard rules:** never tag or run `latest` (a rollback must name exactly what was running).
  Before every recreate, copy the sqlite DB with its `-wal` and `-shm` siblings into the
  stack's `backups/` folder (the bare `.db` is stale in WAL mode). The compose edit preserves
  `QUEUE_WORKERS=1`, the memory/cpu limits and all data/env/cache mounts. Secrets (HF token env
  file, remote SSH service key mounted read-only) stay out of the image and every tracked file;
  never read or print them. Rollback is restoring the previous `image:` tag and recreating.
- **Owner go required for:** credential or secret changes, and any DB change beyond an
  additive migration (dropping or rewriting columns, tables or rows).
- **Verify:** `docker inspect` shows the new tag on both containers; the root answers 200;
  `docker exec` of `env | grep QUEUE_WORKERS` prints `1`; the backup path is reported.

## Product language

User-interface copy stays in English to match upstream and keep changes
straightforward to merge in a pull request.

## Optional authentication disable mode

Set `DISABLE_AUTH` to `1` or `true` to bypass authentication and run every
request as the single admin user. This is intended only for single-user,
trusted-LAN deployments and defaults to off.

## Transcription profile execution fields

Transcription profiles store execution selection and remote SSH connection
metadata alongside their existing transcription parameters:

- `execution_mode` is text, accepts `local` or `remote`, and defaults to
  `local`. It selects where the profile's transcription workload runs.
- `remote_host` is text containing the SSH host name used for remote execution.
- `remote_port` is an integer SSH port and defaults to `22` when unset.
- `remote_user` is text containing the SSH login user.
- `remote_key_path` is text containing a filesystem path to an SSH private key.
  Profiles never store private key material.
- `remote_work_dir` is text containing the remote staging directory.
- `remote_command_prefix` is optional text prepended to each remote command and
  defaults to empty. It supports SSH servers that need an additional execution
  hop, for example `wsl -d Ubuntu --`.
- `remote_connect_timeout_seconds` is an integer connection timeout and
  defaults to `10` seconds when unset.

The profile create and update APIs require `remote_host`, `remote_user`, and
`remote_key_path` whenever `execution_mode` is `remote`.

A possible follow-up is adding a test-connection action to the transcription
profile form; it is not part of the current execution-field UI.

## Remote WhisperX transport contract

Remote WhisperX execution uses Go-native `golang.org/x/crypto/ssh` only. The
remote contract consists entirely of commands with stdin/stdout streams, so an
SFTP dependency, an `openssh-client` image addition, and Dockerfile divergence
are unnecessary. The executor reads the private key from `remote_key_path` at
job time; key material is never logged or persisted in the database.

The executor submits audio on stdin to one invocation of
`<prefix> <remote_work_dir>/scriberr-remote-job.sh`, retrieves the wrapper's
`output` directory as a tar stream from a second SSH exec, and removes
`<remote_work_dir>/jobs/<job-id>` with a third exec on every success and failure
path. Retrieved files enter the existing WhisperX output parser unchanged, and
remote stderr is written to the job's existing `transcription.log`.

Host-key verification uses the first conventional known-hosts file available:
`~/.ssh/known_hosts`, then `/etc/ssh/ssh_known_hosts`. When neither exists, the
initial implementation uses `ssh.InsecureIgnoreHostKey`; this is an explicit
single-user LAN deployment trade-off that avoids requiring new configuration
before a host-key management UI or setting exists.

Remote execution falls back to local CPU only when the remote service is
unavailable: connection refusal, host lookup failure, SSH authentication
failure, connection timeout, or a missing remote wrapper (exit status 127).
Once the wrapper runs, its job-level exit statuses 2, 3, and 4 fail the job
without local fallback so configuration and transcription errors stay visible.

Each transcription job stores `execution_path` as `remote`, `local`, or
`local-fallback`, plus a short `execution_reason`. Both fields are returned by
the job API. The job view displays a badge for remote and fallback execution;
plain local execution remains visually unchanged.
