.PHONY: build install test check lint fmt

build:
	go build -o bin/goextractlint ./cmd/goextractlint

install:
	go install ./cmd/goextractlint

test:
	go test ./...

check:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	golines --max-len=120 --write-output cmd internal
	gofumpt -w cmd internal
