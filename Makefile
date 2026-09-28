BINARY := bin/myhome-chromecast
REGISTRY ?= raspberrypi.lan:5000
IMAGE_NAME ?= org.monroe.team/myhome-chromecast
IMAGE ?= $(REGISTRY)/$(IMAGE_NAME)
TAG ?= latest
PLATFORM ?= linux/arm64
DOCKER_IMAGE := $(IMAGE):$(TAG)

.PHONY: build run build-arm64 docker-build docker-push fmt vet

build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o $(BINARY) ./cmd/myhome-chromecast

run:
	CGO_ENABLED=0 go run ./cmd/myhome-chromecast

build-arm64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o bin/myhome-chromecast-linux-arm64 ./cmd/myhome-chromecast

docker-build:
	docker buildx build --platform $(PLATFORM) --load -t $(DOCKER_IMAGE) .

docker-push: docker-build
	docker push $(DOCKER_IMAGE)

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vet:
	go vet ./...
