.PHONY: test test-server test-agent test-web docker-config

test: test-server test-agent test-web docker-config

test-server:
	cd server && go test ./...

test-agent:
	cd agent && cargo test

test-web:
	cd web && npm test -- --run && npm run build

docker-config:
	docker compose config >/dev/null
