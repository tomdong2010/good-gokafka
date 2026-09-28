PROTOC ?= protoc

.PHONY: build test vet proto up down

build:
	go build -o bin/producer ./producer
	go build -o bin/consumer ./consumer

test:
	go test -race ./...

vet:
	go vet ./...

# Requires protoc and protoc-gen-go (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest).
proto:
	$(PROTOC) -I internal/message/pb --go_out=internal/message/pb --go_opt=paths=source_relative internal/message/pb/message.proto

up:
	docker compose up -d

down:
	docker compose down
