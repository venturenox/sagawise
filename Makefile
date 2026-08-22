.PHONY: start status stop restart clean api_examples order_flow

start:
	docker network create shared_network || true
	UUID=$(shell whoami)$(shell hostname) docker compose up -d --build

status:
	docker ps

stop:
	docker compose down

restart:
	make stop && make start

clean:
	docker compose down -v --rmi local || true
	cd examples/api_examples && docker compose down -v --rmi local || true
	cd examples/order_flow && docker compose down -v --rmi local || true
	docker network remove shared_network || true

api_examples:
	make clean && docker network create shared_network && make start && cd examples/api_examples && docker compose up -d --build

order_flow:
	docker network create shared_network || true
	make start && cd examples/order_flow && UUID=$(shell whoami)$(shell hostname) docker compose up -d --build