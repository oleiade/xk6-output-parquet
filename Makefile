MAKEFLAGS += --silent

# `xk6 build --with` accepts <module>=<local-path>. We discover the module
# from go.mod so this Makefile keeps working after a rename or fork.
MODULE := $(shell go list -m)

.PHONY: all
all: clean format lint test build

## help: Print this help.
.PHONY: help
help:
	echo "Usage: make <target>"
	echo "Targets:"
	echo "  clean   Remove build artefacts (./k6)"
	echo "  format  go fmt ./..."
	echo "  vet     go vet ./..."
	echo "  lint    golangci-lint run ./... (if installed)"
	echo "  test    go test -race -cover ./..."
	echo "  build   xk6 build --with $$(go list -m)=. -> ./k6"
	echo "  run     ./k6 run samples/script.js -o xk6-parquet=./run.parquet"
	echo "  it      Build then run a 1-iteration integration smoke test"
	echo "  tidy    go mod tidy"

## clean: Remove build artefacts.
.PHONY: clean
clean:
	rm -f ./k6 ./coverage.out

## format: Apply go fmt.
.PHONY: format
format:
	go fmt ./...

## vet: go vet.
.PHONY: vet
vet:
	go vet ./...

## tidy: go mod tidy.
.PHONY: tidy
tidy:
	go mod tidy

## lint: golangci-lint run, if installed.
.PHONY: lint
lint:
	if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not installed, skipping"; fi

## test: Unit tests with race + coverage.
.PHONY: test
test:
	go test -race -cover -coverprofile=coverage.out ./...

## build: Build k6 with this extension linked in.
.PHONY: build
build:
	go install go.k6.io/xk6/cmd/xk6@latest
	xk6 build --with $(MODULE)=.

## run: Run the bundled sample script against this output.
.PHONY: run
run: build
	./k6 run samples/script.js -o xk6-parquet=./run.parquet --quiet

## it: 1-iteration integration smoke test.
.PHONY: it
it: build
	./k6 run --iterations 1 --quiet --no-summary -o xk6-parquet=/tmp/xk6-parquet-it.parquet samples/script.js
	@test -s /tmp/xk6-parquet-it.parquet || (echo "expected non-empty parquet file"; exit 1)
	@echo "OK: wrote /tmp/xk6-parquet-it.parquet ($$(wc -c < /tmp/xk6-parquet-it.parquet) bytes)"
