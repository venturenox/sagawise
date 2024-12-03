.PHONY:	start status stop restart api_examples
start:
	UUID=$(shell whoami)$(shell hostname) docker compose up -d --build

status:
	docker ps

stop:
	docker compose down

restart:
	make stop && make start

clean:
	make clean_containers && make clean_image_volume && make clean_network

clean_containers:
	docker container stop $(shell docker container list -aq) || true && docker container remove $(shell docker container list -aq) || true

clean_image_volume:
	docker image remove $(shell docker image list -aq) || true && docker volume remove $(shell docker volume list -q) || true
	
clean_network:
	docker network remove $(shell docker network list -q) || true

api_examples:
	make clean && docker network create shared_network && make start && cd examples/api_examples && docker compose up -d --build
