# Contributing to gomedia

Thanks for taking the time to contribute. This document covers everything you need to build, test, and ship a change.

## Development setup

Requirements:

- Go 1.25 or newer (`go.mod` is the source of truth).
- A C toolchain (`gcc` or `clang`) — required for the CGo subpackages.

Native libraries needed by the CGo decoders:

```bash
# Debian / Ubuntu
sudo apt-get install -y \
  libfdk-aac-dev \
  libopus-dev libopusfile-dev \
  libavcodec-dev libavutil-dev libswscale-dev libavformat-dev libswresample-dev
```

Hardware-accelerated decoders are optional and gated behind build tags:

| Package                  | Build tag | Requires                          |
|--------------------------|-----------|-----------------------------------|
| `decoder/video/rkmpp`    | `rkmpp`   | Rockchip MPP, `librga`            |
| `decoder/video/cuda`     | `cuda`    | NVIDIA Video Codec SDK            |

## Common tasks

```bash
make test       # go test ./...
make lint       # golangci-lint run ./...
make vet        # go vet ./...
make cover      # produce coverage.out + textual summary
make generate   # regenerate mocks (installs mockgen first)
```

`golangci-lint` is required for the `lint` target. Install with:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

## Code conventions

- **Interfaces live in the root package.** Implementations in `codec/`, `decoder/`, `encoder/`, `format/`, `reader/`, `writer/` must not import each other.
- **No `make([]byte, n)` on hot paths.** Use `utils/buffer.RingAlloc` (or `GrowingRingAlloc`) for packet data and `utils/buffer.PooledBuffer` for scratch space. Producers call `Alloc`, consumers receive a `SlotHandle` via `Packet.Clone(false)`, every owner calls `Release` exactly once.
- **Async components** embed one of the lifecycle managers from `utils/lifecycle` (`AsyncManager`, `FailSafeAsyncManager`, or `DefaultManager`) and implement `Step(stopCh)`.
- **Logging** is injected via the `WithLogger` functional option. Library packages must depend only on the `utils/logger.Logger` interface, never on a concrete logger.
- **Magic numbers** carry a `//nolint:mnd` directive with a real justification (spec section, RFC, etc.). Empty justifications are rejected by `nolintlint`.
- **Mocks** live in `mocks/` and are fully generated. Do not hand-edit; regenerate with `make generate`.

## Tests

- Run the full suite with `make test` before submitting.
- Codec tests use JSON fixtures under `tests/data/`. Helpers are in `tests/utils.go`.
- Do not write tests for `String()` methods.
- When a test fails, determine whether the bug is in the code or in the test. If the code is wrong, fix the code — do not weaken the test.

## Pull request checklist

- `make test` passes.
- `make lint` is clean (or each new `//nolint` directive has a real reason).
- Public API surface in `gomedia.go` and subpackage exported types is unchanged, or the change is called out in the PR description and `CHANGELOG.md`.
- New behaviour has a test.
- `mocks/` diff is either empty or the result of `make generate`.

## Reporting bugs and security issues

- Functional bugs: open a GitHub issue using the bug template.
- Security issues: see [SECURITY.md](SECURITY.md). Do not file public issues for vulnerabilities.
