-include .env

GOOSE_DRIVER := postgres
GOOSE_DBSTRING := $(DATABASE_URL)
GOOSE_MIGRATION_DIR := $(CURDIR)/migrations

export GOOSE_DRIVER
export GOOSE_DBSTRING
export GOOSE_MIGRATION_DIR

.PHONY: migrate-status migrate-up migrate-down migrate-new

migrate-status:
	goose status

migrate-up:
	goose up

migrate-down:
	goose down

migrate-new:
	goose create $(name) sql