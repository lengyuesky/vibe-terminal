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
