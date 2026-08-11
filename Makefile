.DEFAULT_GOAL := check

.PHONY: build check fmt generate install lint release-check test verify vet

build:
	go build -v ./...

fmt:
	gofmt -s -w -e .
	terraform fmt -recursive examples

test:
	go test -race -cover ./...

verify:
	go mod verify
	cd tools && go mod verify

vet:
	go vet ./...

lint:
	golangci-lint run

generate:
	cd tools && go generate ./...

release-check:
	goreleaser check
	goreleaser release --snapshot --clean --skip=sign
	./scripts/verify-release-assets.sh dist --allow-unsigned

install:
	go install -v ./...

check: fmt verify build vet test
