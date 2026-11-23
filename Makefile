REGISTRY ?= docker.io/lammw12
TAG      ?= v0.1.0
PLATFORM ?= linux/amd64
GOPROXY  ?= https://proxy.golang.org,direct

.PHONY: web-build docs-build build-assets test build-push deploy undeploy

web-build:
	cd web && npm ci && npm run build
	rm -rf cmd/server/ui/* && cp -R web/dist/* cmd/server/ui/ && touch cmd/server/ui/.gitkeep

docs-build:
	cd docs-hugo && hugo --minify --baseURL /docs/ -d ../cmd/server/docs
	touch cmd/server/docs/.gitkeep

build-assets: web-build docs-build

test:
	docker run --rm -v "$(PWD):/src" -w /src -e GOPROXY=$(GOPROXY) golang:1.26-bookworm \
		sh -c "go test -v -race ./pkg/..."

build-push:
	docker buildx build --platform $(PLATFORM) \
		-f deploy/docker/Dockerfile.server -t $(REGISTRY)/calico-netdiag-server:$(TAG) --push .
	docker buildx build --platform $(PLATFORM) \
		-f deploy/docker/Dockerfile.agent -t $(REGISTRY)/calico-netdiag-agent:$(TAG) --push .
	docker buildx build --platform $(PLATFORM) \
		-f deploy/docker/Dockerfile.probe -t $(REGISTRY)/calico-netdiag-probe:$(TAG) --push .

deploy:
	kubectl apply -f deploy/k8s/

undeploy:
	kubectl delete -f deploy/k8s/ --ignore-not-found
