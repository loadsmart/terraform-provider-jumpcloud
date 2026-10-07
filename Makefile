default: fmt lint test build

build:
	go build -v ./...

install: build
	go install -v ./...

fmt:
	gofmt -s -w -e .

lint:
	golangci-lint run

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

# Acceptance tests create real objects in the JumpCloud org behind JUMPCLOUD_API_KEY.
testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: default build install fmt lint test testacc
