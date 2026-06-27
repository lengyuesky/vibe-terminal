# vibe-terminal

`vibe-terminal` lets a browser reconnect to terminal sessions running on controlled machines. The controlled machine runs `vibe-agent`, which actively connects to the Go server. The server exposes REST and WebSocket APIs for the React/xterm.js web UI.

## Local checks

```bash
make test
```

## MVP boundaries

- Commands run on the controlled machine through the Rust agent.
- The Go server owns authentication, device/session metadata, routing, audit indexes, and workspace container lifecycle.
- The web app does not persist critical state; it restores state from the server.

## Development workflow

Server:

```bash
cd server
go test ./...
go run ./cmd/server
```

Agent:

```bash
cd agent
cargo test
cargo run -- register --server http://localhost:8080 --token dev-token
cargo run -- run
```

Web:

```bash
cd web
npm install
npm test -- --run
npm run dev
```

Full checks:

```bash
make test
```
