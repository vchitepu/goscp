SHELL := /bin/bash
BINARY=goscp

.PHONY: build test bench lint clean

build:
	go build -o bin/$(BINARY) ./cmd/goscp

test:
	go test ./...

bench:
	@set -euo pipefail; \
	go test -bench=. -benchmem -benchtime=30s -count=3 ./pkg/transfer/... | tee docs/bench_report.txt
	@python3 scripts/bench_acceptance.py docs/bench_report.txt
	@go test -race ./pkg/transfer/... >/dev/null
	@echo "PASS: race check clean"

lint:
	golangci-lint run ./...

clean:
	rm -rf bin coverage.out
