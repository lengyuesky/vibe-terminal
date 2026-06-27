# vibe-terminal Deployment

## Server

Create an environment file on the VPS:

```bash
export VIBE_SESSION_SECRET="$(openssl rand -base64 32)"
export VIBE_ADMIN_USER="admin"
export VIBE_ADMIN_PASSWORD="replace-with-a-long-password"
```

Start the server:

```bash
docker compose up -d --build
```

Use Caddy or Nginx in front of port `8080` and expose only HTTPS.

## Agent

Register a controlled machine:

```bash
vibe-agent register --server https://terminal.example.com --token <token-from-web>
vibe-agent run
```

Linux can install `deploy/systemd/vibe-agent.service`. macOS can install `deploy/launchd/com.vibe-terminal.agent.plist`. WSL can run `deploy/scripts/vibe-agent-wsl.sh`.

## Smoke Test

1. Open the web UI.
2. Log in as the administrator.
3. Create an agent registration token.
4. Register and run one agent on Linux, macOS, or WSL.
5. Confirm the device appears online.
6. Open two terminal tabs.
7. Refresh the browser and confirm the tabs can reconnect.
8. Restart the server container and confirm the agent syncs running sessions.
