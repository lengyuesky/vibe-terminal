.PHONY: test test-server test-agent test-web docker-config

ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
export RUSTUP_HOME ?= $(ROOT).tools/rustup
export CARGO_HOME ?= $(ROOT).tools/cargo
export GOCACHE ?= $(ROOT).tools/go-build-cache
export GOPATH ?= $(ROOT).tools/go-path
export PATH := $(CARGO_HOME)/bin:$(PATH)

test: test-server test-agent test-web docker-config

test-server:
	cd server && go test ./...

test-agent:
	cd agent && cargo test

test-web:
	cd web && npm test -- --run && npm run build

docker-config:
	docker compose config >/dev/null
