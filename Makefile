VERSION := $(shell cat VERSION)
LDFLAGS := -X github.com/Mathias-g/Servitor/internal/app.Version=$(VERSION)

.PHONY: all build test check fmt vet lint clean release

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

# release bumps VERSION to <new-version>, rebuilds, and prints the git tag and
# push commands. It does not tag or push; that is left to the operator so the
# build can be verified first.
# The version argument arrives as a make goal; a fallthrough rule consumes it
# so make does not complain about an unknown target.
release:
	@test -n "$(filter-out $@,$(MAKECMDGOALS))" || { echo "usage: make release <new-version> (e.g. make release 0.2.0)"; exit 2; }
	@echo "$(filter-out $@,$(MAKECMDGOALS))" > VERSION
	@$(MAKE) build
	@echo ""
	@echo "Built bin/servitor as version $$(cat VERSION)."
	@echo "Next:"
	@echo "  git add VERSION"
	@echo "  git commit -m \"Release v$$(cat VERSION)\""
	@echo "  git tag v$$(cat VERSION)"
	@echo "  git push origin main && git push origin v$$(cat VERSION)"

%:
	@:

clean:
	rm -rf bin
