IMAGE    ?= dockette/viewdoc
include VERSION
VARIANTS := control-center viewer-kasm viewer-webtop

.PHONY: build test run up down logs ps push cert version release

cert:
	go run ./cmd/gen-cert

build:
	VIEWDOC_VERSION=$(VERSION) docker compose build

run:
	docker compose up

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

# Pushes <variant> and <variant>-$(VERSION) for every image (CI does this on
# master; this is for manual publishing of a local build).
push:
	@for v in $(VARIANTS); do \
	  docker tag $(IMAGE):$$v $(IMAGE):$$v-$(VERSION) && \
	  docker push $(IMAGE):$$v && \
	  docker push $(IMAGE):$$v-$(VERSION) || exit 1; \
	done

version:
	@echo $(VERSION)

# Sets VERSION to today's date (UTC). Commit and push to master to publish:
# CI then pushes <variant>-<VERSION> images and creates the GitHub release.
release:
	@sed -i.bak "s/^VERSION=.*/VERSION=$$(date -u +%Y.%m.%d)/" VERSION && rm VERSION.bak
	@cat VERSION
