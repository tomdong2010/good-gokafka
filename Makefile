PROTOC ?= protoc

.PHONY: help release-snapshot build test integration lint proto up demo monitoring load down send

help: ## Show this help
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build both binaries into ./bin
	go build -o bin/producer ./producer
	go build -o bin/consumer ./consumer

test: ## Run unit tests with the race detector
	go test -race ./...

integration: ## Run integration tests against Kafka from docker compose
	docker compose up -d --wait kafka
	go test -tags integration -race -count=1 ./integration/...

release-snapshot: ## Build release archives locally into ./dist (needs goreleaser)
	goreleaser release --snapshot --clean

lint: ## Run golangci-lint
	golangci-lint run ./...

# Requires protoc and protoc-gen-go (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest).
proto: ## Regenerate protobuf code
	$(PROTOC) -I internal/message/pb --go_out=internal/message/pb --go_opt=paths=source_relative internal/message/pb/message.proto

up: ## Start Kafka only (for running the services with go run)
	docker compose up -d --wait kafka

demo: ## Start Kafka, producer and 3 consumers in Docker
	docker compose up -d --build --wait --scale consumer=3

monitoring: ## demo + Prometheus (:9090) + Grafana dashboard (:3001), with 30% simulated failures
	SIMULATE_FAILURE_RATE=0.3 docker compose --profile monitoring up -d --build --wait --scale consumer=3

load: ## Send 500 messages (5% invalid) to the producer
	scripts/load.sh 500

send: ## Send a sample message to the producer
	@curl -s -X POST http://localhost:3000/api/send -H 'content-type: application/json' \
	  -d '{"from":"gopher","content":{"header":"Hi","body":"Hello Kafka"}}'; echo

down: ## Stop everything and delete Kafka data
	docker compose --profile monitoring --profile ui down -v
