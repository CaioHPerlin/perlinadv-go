.DEFAULT_GOAL := help

COMPOSE ?= docker compose
GOOSE ?= goose
DB_SSH ?= ssh -i $(HOME)/.ssh/id_oracle
DB_SSH_HOST ?= perlin@152.67.57.175
DB_ADMINER_PORT ?= 18080
DB_REMOTE_DIR ?= /opt/perlinadv/api
DB_REMOTE_COMPOSE := cd $(DB_REMOTE_DIR) && docker compose --env-file .env.production --env-file .env.release -f compose.production.yml --profile tools

ifneq (,$(wildcard .env))
include .env
endif

GOOSE_DRIVER := postgres
GOOSE_DBSTRING := $(DATABASE_URL)
GOOSE_MIGRATION_DIR := $(CURDIR)/migrations

export GOOSE_DRIVER GOOSE_DBSTRING GOOSE_MIGRATION_DIR

.PHONY: help db-up db-down db-logs db-access db-access-stop migrate-status migrate-up migrate-down migrate-new generate run worker build test vet check

help:
	@echo "make db-up                         			starts PostgreSQL container"
	@echo "make db-access                     			opens production Adminer through SSH"
	@echo "make db-access-stop                			stops production Adminer"
	@echo "make migrate-up                        	applies migrations"
	@echo "make migrate-down                  			reverts the last migration"
	@echo "make migrate-new name=migration_name    	creates a new migration"
	@echo "make run                           			starts the API"
	@echo "make generate                      			regenerates typed SQL with sqlc"
	@echo "make check                         			runs vet and tests"

db-up:
	$(COMPOSE) up -d db

db-down:
	$(COMPOSE) down db

db-logs:
	$(COMPOSE) logs -f db

db-access:
	@$(DB_SSH) $(DB_SSH_HOST) '$(DB_REMOTE_COMPOSE) up -d adminer'
	@echo "Open http://127.0.0.1:$(DB_ADMINER_PORT) and keep this terminal open (Ctrl+C to close the tunnel)."
	@$(DB_SSH) -o ExitOnForwardFailure=yes -N -L 127.0.0.1:$(DB_ADMINER_PORT):127.0.0.1:8080 $(DB_SSH_HOST)

db-access-stop:
	@$(DB_SSH) $(DB_SSH_HOST) '$(DB_REMOTE_COMPOSE) stop adminer'

migrate-status:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	$(GOOSE) status

migrate-up:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	go run ./cmd/migrate

migrate-down:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	$(GOOSE) down

migrate-new:
	@test -n "$(name)" || (echo "usage: make migrate-new name=migration_name"; exit 1)
	$(GOOSE) -dir "$(GOOSE_MIGRATION_DIR)" create "$(name)" sql

generate:
	sqlc generate

run:
	go run ./cmd/api

worker:
	go run ./cmd/worker

build:
	mkdir -p bin
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker
	go build -o bin/admin ./cmd/admin

test:
	go test ./...

vet:
	go vet ./...

check: vet test
