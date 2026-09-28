BINARY := bin/myhome-chromecast

.PHONY: build run build-arm64 fmt vet

build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o $(BINARY) ./cmd/myhome-chromecast

run:
	CGO_ENABLED=0 go run ./cmd/myhome-chromecast

build-arm64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o bin/myhome-chromecast-linux-arm64 ./cmd/myhome-chromecast

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vet:
	go vet ./...
