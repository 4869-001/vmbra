BIN := bin/anywhered
PKG := ./cmd/anywhered
PORT ?= 7788

.PHONY: build app run test vet smoke fmt clean

build:
	@mkdir -p bin
	go build -trimpath -ldflags "-s -w" -o $(BIN) $(PKG)
	@echo "built $(BIN)"

app: build
	@./desktop/build.sh

run:
	go run $(PKG) --port $(PORT)

test:
	go test ./...

vet:
	go vet ./...

smoke:
	./scripts/smoke.sh

fmt:
	gofmt -w ./cmd ./internal

clean:
	rm -rf bin
