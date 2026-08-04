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

The default branch remains `main`, matching and tracking the upstream default.
Do not rename it. Feature work belongs on feature branches so upstream rebases
remain straightforward.

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

## Product language

User-interface copy stays in English to match upstream and keep changes
straightforward to merge in a pull request.
