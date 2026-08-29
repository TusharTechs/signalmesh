.PHONY: setup infra infra-full node cluster dev test demo chaos benchmark dashboard

setup:
	docker compose pull

infra:
	docker compose up -d nats

infra-full:
	docker compose up -d nats postgres redis prometheus grafana

node:
	cd services/signalmesh-node && go run ./cmd/signalmesh

cluster:
	./scripts/dev-cluster.sh

dev:
	docker compose up --build

test:
	cd services/signalmesh-node && go test -race ./...

demo:
	./scripts/demo.sh

chaos:
	./scripts/chaos.sh $(or $(SCENARIO),restore) $(or $(DURATION),30)

benchmark:
	./scripts/benchmark.sh

dashboard:
	cd apps/dashboard && npm run dev