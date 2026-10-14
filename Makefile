.PHONY: templ generate build dev docker-dev docker-prod

templ generate:
	go tool templ generate

build: generate
	go build -tags '!dev' -o bin/raices .

dev:
	ENVIRONMENT=development air -c .air.toml

docker-dev:
	docker compose -f compose.dev.yml up --build -d

docker-prod:
	docker compose -f compose.prod.yml up --build
