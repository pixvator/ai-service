OUTPUT     = ./ai-service
IMAGE_NAME := ai-service
VERSION   := $(or $(shell cat VERSION | tr -d '[:space:]'),dev)
MODULE    := github.com/QuantumNous/new-api

.PHONY: all docker-stop docker-build docker-run

all: docker-stop docker-build docker-run

docker-stop:
	@echo "Stopping docker containers..."
	@sudo -E docker compose -f docker-compose.custom.yml down --rmi all --volumes --remove-orphans

# ── Docker ────────────────────────────────────────────────────────────────────
# docker-build: backend-only binary (no embedded frontend).
# Frontend is deployed separately via web/deploy.sh.
docker-build:
	@echo "Building Go binary for linux/amd64 (no embedded frontend)..."
	@GOEXPERIMENT=greenteagc CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build \
		-ldflags "-s -w -extldflags '-static' -X '$(MODULE)/common.Version=$(VERSION)'" \
		-o $(OUTPUT)
	@sudo -E docker build --platform linux/amd64 -f Dockerfile.custom \
		-t $(IMAGE_NAME):$(VERSION) \
		-t $(IMAGE_NAME):latest \
		.
	@echo "Built $(IMAGE_NAME):$(VERSION)"

docker-run:
	@echo "Running $(IMAGE_NAME):$(VERSION) in docker..."
	@sudo -E docker compose -f docker-compose.custom.yml up -d
