IMAGE  ?= dockette/viewdoc

.PHONY: build up down logs ps test push cert

cert:
	go run ./cmd/gen-cert

build:
	docker compose build

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f --tail=200

ps:
	docker compose ps

test:
	go test ./...

push:
	docker push $(IMAGE):control-center
	docker push $(IMAGE):viewer-kasm
	docker push $(IMAGE):viewer-webtop
