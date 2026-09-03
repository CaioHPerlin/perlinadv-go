.DEFAULT_GOAL := help

COMPOSE ?= docker compose
GOOSE ?= goose
SQLC ?= sqlc

ifneq (,$(wildcard .env))
include .env
endif

GOOSE_DRIVER := postgres
GOOSE_DBSTRING := $(DATABASE_URL)
GOOSE_MIGRATION_DIR := $(CURDIR)/migrations

export GOOSE_DRIVER GOOSE_DBSTRING GOOSE_MIGRATION_DIR

.PHONY: help db-up db-down db-logs generate migrate-status migrate-up migrate-down migrate-new run build test vet check

help:
	@echo "make db-up                         			starts PostgreSQL container"
	@echo "make generate                      			runs sqlc generate"
	@echo "make migrate-up                        	applies migrations"
	@echo "make migrate-down                  			reverts the last migration"
	@echo "make migrate-new name=migration_name    	creates a new migration"
	@echo "make run                           			starts the API"
	@echo "make check                         			runs vet and tests"

db-up:
	$(COMPOSE) up -d db

db-down:
	$(COMPOSE) down db

db-logs:
	$(COMPOSE) logs -f db

generate:
	$(SQLC) generate

migrate-status:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	$(GOOSE) status

migrate-up:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	$(GOOSE) up

migrate-down:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	$(GOOSE) down

migrate-new:
	@test -n "$(name)" || (echo "usage: make migrate-new name=migration_name"; exit 1)
	$(GOOSE) -dir "$(GOOSE_MIGRATION_DIR)" create "$(name)" sql

run:
	go run ./cmd/api

build:
	mkdir -p bin
	go build -o bin/api ./cmd/api

test:
	go test ./...

vet:
	go vet ./...

check: vet test