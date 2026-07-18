# GoSCP — A fast, concurrent, Go-native file copy tool over SSH

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](#license)
[![CI](https://github.com/vchitepu/goscp/actions/workflows/ci.yml/badge.svg)](https://github.com/vchitepu/goscp/actions/workflows/ci.yml)

GoSCP is a high-performance SSH file transfer tool written fully in Go.
It is built as both a CLI and an embeddable library, with chunked transfers,
concurrency, checkpoint/resume, and ProxyJump support.

## Features

| Capability | GoSCP | mscp |
|---|---:|---:|
| Pure Go (no CGO) | ✅ | ❌ |
| Concurrent chunked transfers | ✅ | ✅ |
| ProxyJump (`-J` equivalent) support | ✅ | ⚠️ (depends on external SSH tooling) |
| Embeddable Go library API | ✅ | ❌ |
| Checkpoint + resume | ✅ | ✅ |
| Remote-to-remote transfer mode | ✅ | ✅ |

## Install

```bash
go install github.com/vchitepu/goscp/cmd/goscp@latest
```

## Usage

Basic local to remote copy:

```bash
goscp ./local/file.bin user@host:/remote/path/file.bin
```

Remote to local copy:

```bash
goscp user@host:/remote/file.bin ./downloads/file.bin
```

Remote to remote copy:

```bash
goscp user@source:/data/file.bin user@dest:/backup/file.bin
```

Copy multiple sources to a destination directory:

```bash
goscp ./a.txt ./b.txt user@host:/remote/dir/
```

Tune concurrency and chunking:

```bash
goscp -n 8 -s 2 -S 64M ./big.img user@host:/data/big.img
```

Limit bandwidth:

```bash
goscp -L 200 ./dataset.tar user@host:/data/dataset.tar
```

Enable checkpointing:

```bash
goscp --checkpoint ./huge.bin user@host:/data/huge.bin
```

Resume from checkpoint ID:

```bash
goscp --resume <checkpoint_id> ./huge.bin user@host:/data/huge.bin
```

Dry-run transfer plan:

```bash
goscp --dry-run ./source user@host:/dest
```

SSH options and identity file:

```bash
goscp -i ~/.ssh/id_ed25519 -P 2222 -o StrictHostKeyChecking=yes ./file user@host:/dst
```

## Embedding as a library

GoSCP packages are importable in your own programs. For transfer scheduling and workers:

```go
package main

import (
	"context"
	"log"

	"github.com/vchitepu/goscp/pkg/transfer"
)

func main() {
	scheduler := transfer.NewDefaultScheduler(transfer.SchedulerConfig{
		NumWorkers: 4,
		LimitMbps:  0,
	})

	_ = scheduler
	_ = context.Background()
	log.Println("imported goscp transfer package successfully")
}
```

See package `pkg/transfer` and `ARCHITECTURE.md` for interface contracts and end-to-end data flow.

## Architecture

Architecture and module contracts are documented in:

- [ARCHITECTURE.md](./ARCHITECTURE.md)

## Contributing

Contributions are welcome.

1. Fork the repo and create a topic branch.
2. Add tests for new behavior (unit and integration where appropriate).
3. Run checks locally:
   - `go test ./...`
   - `go test -tags integration ./test/integration/...`
   - `go build ./...`
4. Open a PR with clear scope and rationale.

## License

Licensed under Apache License 2.0. See [LICENSE](./LICENSE).
