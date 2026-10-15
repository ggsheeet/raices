.PHONY: templ generate build dev docker-dev docker-prod deploy

IMAGE := us-central1-docker.pkg.dev/white-stacker-453005-n4/raices/raices
TAG := $(shell date -u +%Y%m%d-%H%M%S)

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

deploy:
	docker build --platform=linux/amd64 --target production -t $(IMAGE):$(TAG) -t $(IMAGE):latest .
	docker push $(IMAGE):$(TAG)
	docker push $(IMAGE):latest
	gcloud run deploy raices --image $(IMAGE):$(TAG) --region us-central1 --max-instances 1 --execution-environment gen2 --no-cpu-throttling --port 8080 --quiet
