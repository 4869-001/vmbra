BIN := bin/anywhered
PKG := ./cmd/anywhered
PORT ?= 7788

.PHONY: build link app run test vet smoke fmt clean

build:
	@mkdir -p bin
	go build -trimpath -ldflags "-s -w" -o $(BIN) $(PKG)
	@echo "built $(BIN)"

# 生成客户开通链接：make link HOST=1.2.3.4 PORT=22 USER=root PASSWORD=***
link: build
	@go build -trimpath -ldflags "-s -w" -o bin/vmbra-link ./cmd/vmbra-link
	@./bin/vmbra-link --host "$(HOST)" --port "$(or $(PORT),22)" --user "$(or $(USER),root)" \
		--password "$(PASSWORD)" --name "$(NAME)" --tools "$(TOOLS)" $(if $(VM),--vm)

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
