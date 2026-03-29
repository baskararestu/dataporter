.PHONY: up down build test lint clean swag

# Start local dev environment (2 PG + app)
up:
	docker compose --profile local up --build -d

# Stop all containers
down:
	docker compose --profile local down

# Build binary locally
build:
	go build -o dataporter .

# Run all tests
test:
	go test -v -race -count=1 ./...

# Run tests with coverage
coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Lint
lint:
	golangci-lint run ./...

# Clean build artifacts
clean:
	rm -f dataporter coverage.out coverage.html

# Tidy dependencies
tidy:
	go mod tidy

# Regenerate Swagger docs
SWAG := $(shell which swag 2>/dev/null || echo $(HOME)/go/bin/swag)
swag:
	$(SWAG) init --generalInfo main.go --output docs --parseDependency --parseInternal

# View logs
logs:
	docker compose logs -f migration
