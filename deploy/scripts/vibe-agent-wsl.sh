#!/usr/bin/env bash
set -euo pipefail
exec "${VIBE_AGENT_BIN:-$HOME/.local/bin/vibe-agent}" run
