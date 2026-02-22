BINARY ?= quadsyncd

.PHONY: all fmt test lint vuln build clean install

all: fmt test lint build

fmt:
	@echo "==> Formatting code..."
	@gofmt -w -s .

test:
	@echo "==> Running tests..."
	@go test -v -race ./...

lint:
	@echo "==> Running linter..."
	@go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run

vuln:
	@echo "==> Checking for vulnerabilities..."
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./...

build:
	@echo "==> Building binary..."
	@go build -trimpath -o $(BINARY) ./cmd/quadsyncd

clean:
	@echo "==> Cleaning..."
	@rm -f $(BINARY)
	@rm -rf dist/

install: build
	@echo "==> Installing to ~/.local/bin/..."
	@mkdir -p ~/.local/bin
	@cp $(BINARY) ~/.local/bin/
	@echo "==> Installing systemd user units..."
	@mkdir -p ~/.config/systemd/user
	@cp packaging/systemd/user/*.service ~/.config/systemd/user/
	@cp packaging/systemd/user/*.timer ~/.config/systemd/user/
	@cp packaging/systemd/user/*.socket ~/.config/systemd/user/ || true
	@echo "==> Done! Run 'systemctl --user daemon-reload' to load units"
