.PHONY: setup infra node dev test demo chaos benchmark

setup:
	docker compose pull

infra:
	docker compose up -d nats postgres redis prometheus grafana

node:
	cd services/signalmesh-node && go run ./cmd/signalmesh

dev:
	docker compose up --build

test:
	cd services/signalmesh-node && go test -race ./...

demo:
	@echo "TODO: make demo"

chaos:
	@echo "TODO: make chaos"

benchmark:
	@echo "TODO: make benchmark"
