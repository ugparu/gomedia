.PHONY: generate test

generate:
	go install go.uber.org/mock/mockgen@latest
	go generate ./...

test:
	go test ./...
