BIN     := mailsender
LDFLAGS := -s -w

.PHONY: all web build run dev-smtp release

all: build

web:
	cd web && npm ci && npm run build

# Single static binary with the console embedded (no CGO needed: pure-Go SQLite).
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/mailsender

run: build
	./bin/$(BIN)

# Three fake SMTP servers on 2525-2527 (user/pass: test/test); 2527 always fails.
dev-smtp:
	go run ./cmd/devsmtp -base 2525 -n 3 -fail 2527

release: web
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-linux-amd64 ./cmd/mailsender
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-linux-arm64 ./cmd/mailsender
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-darwin-arm64 ./cmd/mailsender
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-windows-amd64.exe ./cmd/mailsender
