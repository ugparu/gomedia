.PHONY: generate test lint vet cover tidy

generate:
	go install go.uber.org/mock/mockgen@latest
	go generate ./...

test:
	go test ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

tidy:
	go mod tidy
