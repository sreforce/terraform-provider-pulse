.DEFAULT_GOAL := check

.PHONY: build check fmt generate install lint test vet

build:
	go build -v ./...

fmt:
	gofmt -s -w -e .
	terraform fmt -recursive examples

test:
	go test -race -cover ./...

vet:
	go vet ./...

lint:
	golangci-lint run

generate:
	cd tools && go generate ./...

install:
	go install -v ./...

check: fmt build vet test

