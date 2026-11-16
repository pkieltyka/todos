.PHONY: build test check install clean help

build:
	mkdir -p bin
	go build -o bin/todos .

test:
	go test ./...

check:
	@set -eu; unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then printf '%s\n' "$$unformatted"; exit 1; fi
	go vet ./...
	go test -race ./...

install:
	go install .

clean:
	rm -rf bin

help:
	@printf '%s\n' \
	  'build    Build ./bin/todos (default)' \
	  'test     Run tests without the race detector' \
	  'check    Check formatting, vet and race tests' \
	  'install  Install todos with go install' \
	  'clean    Remove ./bin/' \
	  'help     Show available targets'
