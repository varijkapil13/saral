BIN := kanso
PKG := ./...
LDFLAGS := -s -w

.PHONY: build test race lint tidy bench cover run size clean check

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/kanso

test:
	go test -count=1 $(PKG)

race:
	go test -race -count=1 $(PKG)

lint:
	golangci-lint run

tidy:
	go mod tidy && git diff --exit-code go.mod go.sum

bench:
	go test -run '^$$' -bench . -benchmem $(PKG)

cover:
	go test -coverprofile=coverage.out $(PKG) && go tool cover -func=coverage.out | tail -1

size: build
	@ls -lh $(BIN) | awk '{print "binary: " $$5}'

run: build
	./$(BIN)

check: tidy lint race
	@echo "ok"

clean:
	rm -f $(BIN) coverage.out
