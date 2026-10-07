default: fmt lint test build docs

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

# Regenerates docs/ from the provider schema and examples/.
docs:
	terraform fmt -recursive examples/
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate --provider-name jumpcloud

# Acceptance tests create real objects in the JumpCloud org behind JUMPCLOUD_API_KEY.
testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: default build install fmt lint test testacc docs
