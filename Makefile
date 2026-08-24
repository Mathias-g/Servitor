VERSION := $(shell cat VERSION)
LDFLAGS := -X github.com/Mathias-g/Servitor/internal/app.Version=$(VERSION)

.PHONY: all build test check fmt vet lint clean

all: build

build:
	go build -o bin/servitor -ldflags "$(LDFLAGS)" ./cmd/servitor

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

vet:
	go vet ./...

lint:
	golangci-lint run

check: test vet lint
	@test -z "$$(gofmt -l .)" || { echo "gofmt -l reports unformatted files:"; gofmt -l .; exit 1; }

clean:
	rm -rf bin
