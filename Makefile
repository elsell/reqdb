.PHONY: build

build:
	mkdir -p build
	go build -o build/reqdb ./cmd/reqdb
