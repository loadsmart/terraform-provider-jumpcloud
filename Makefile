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
	go test -v -cover -timeout=5m -parallel=10 ./...

docs:
	terraform fmt -recursive examples/
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate --provider-name jumpcloud

.PHONY: default build install fmt lint test docs
