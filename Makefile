VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist

.PHONY: all build run clean test vet fmt

all: build

## 构建当前平台到 dist/
build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/sshtool .
	@ls -lh $(DIST)/sshtool

## 构建后直接运行
run: build
	@$(DIST)/sshtool

## 交叉编译三个平台
build-all:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/sshtool-windows-amd64.exe .
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/sshtool-linux-amd64 .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/sshtool-darwin-arm64 .
	@ls -lh $(DIST)

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf $(DIST)
