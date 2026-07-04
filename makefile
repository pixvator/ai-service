OUTPUT     = ./ai-service
IMAGE_NAME := ai-service
VERSION   := $(shell cat VERSION | tr -d '[:space:]')
MODULE    := github.com/QuantumNous/new-api

.PHONY: all docker-stop prepare-embed build-backend docker-build docker-run

all: docker-stop docker-build docker-run

docker-stop:
	@echo "Stopping docker containers..."
	@sudo -E docker compose -f docker-compose.custom.yml down --rmi all --volumes --remove-orphans

prepare-embed:
	@rm -rf web/default/dist web/classic/dist
	@mkdir -p web/default/dist web/classic/dist
	@printf '%s\n' '<!doctype html><html><head><title>placeholder</title></head><body>frontend served separately</body></html>' > web/default/dist/index.html
	@printf '%s\n' '<!doctype html><html><head><title>placeholder</title></head><body>frontend served separately</body></html>' > web/classic/dist/index.html

# ── Backend ───────────────────────────────────────────────────────────────────
# build-backend: standalone binary, frontend served separately (nginx/CDN)
build-backend: prepare-embed
	@echo "Building backend (no embedded frontend)..."
	@CGO_ENABLED=0 go build \
		-ldflags "-s -w -X '$(MODULE)/common.Version=$(VERSION)'" \
		-o $(OUTPUT)

# ── Docker ────────────────────────────────────────────────────────────────────
# docker-build: backend-only binary (no embedded frontend).
# Frontend is deployed separately via web/deploy.sh.
docker-build: prepare-embed
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
