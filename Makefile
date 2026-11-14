.PHONY: build test check install clean help

build:
	go build -o todos .

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
	rm -f todos

help:
	@printf '%s\n' \
	  'build    Build ./todos (default)' \
	  'test     Run tests without the race detector' \
	  'check    Check formatting, vet and race tests' \
	  'install  Install todos with go install' \
	  'clean    Remove ./todos' \
	  'help     Show available targets'
