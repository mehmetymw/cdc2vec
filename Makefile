.PHONY: buil

build:
	mkdir -p bin
	GO111MODULE=on go build -o bin/$(APP) ./cmd/cdc2vec

test:
	GO111MODULE=on go test ./...

lint:
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

run-pg:
	GO111MODULE=on CONFIG_PATH=$(PWD)/configs/postgres-qdrant.yaml go run ./cmd/cdc2vec

run:
	GO111MODULE=on CONFIG_PATH=$(PWD)/configs/config.example.yaml go run ./cmd/cdc2vec
