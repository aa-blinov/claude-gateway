VERSION  ?= dev
LDFLAGS  := -ldflags="-s -w"
DIST     := dist

.PHONY: build build-all clean test

test:
	go test -v ./...

build:
	go build $(LDFLAGS) -o $(DIST)/claude-gateway .

build-all: clean
	mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64  go build $(LDFLAGS) -o $(DIST)/claude-gateway-windows-amd64.exe .
	GOOS=windows GOARCH=arm64  go build $(LDFLAGS) -o $(DIST)/claude-gateway-windows-arm64.exe .
	GOOS=linux   GOARCH=amd64  go build $(LDFLAGS) -o $(DIST)/claude-gateway-linux-amd64       .
	GOOS=linux   GOARCH=arm64  go build $(LDFLAGS) -o $(DIST)/claude-gateway-linux-arm64       .
	GOOS=darwin  GOARCH=amd64  go build $(LDFLAGS) -o $(DIST)/claude-gateway-darwin-amd64      .
	GOOS=darwin  GOARCH=arm64  go build $(LDFLAGS) -o $(DIST)/claude-gateway-darwin-arm64      .

clean:
	rm -rf $(DIST)
