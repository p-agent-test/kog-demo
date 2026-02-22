#!/bin/bash
# Install platform-agent as a systemd user service
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT_DIR="$(dirname "$SCRIPT_DIR")"
BINARY="$AGENT_DIR/bin/platform-agent"
ENV_FILE="$AGENT_DIR/.env"
SERVICE_FILE="$HOME/.config/systemd/user/platform-agent.service"

# Check binary exists
if [ ! -f "$BINARY" ]; then
  echo "Binary not found at $BINARY — run 'make build' first"
  exit 1
fi

# Check .env exists
if [ ! -f "$ENV_FILE" ]; then
  echo ".env not found at $ENV_FILE — copy from .env.example"
  exit 1
fi

# Create systemd user dir
mkdir -p "$(dirname "$SERVICE_FILE")"

cat > "$SERVICE_FILE" << EOF
[Unit]
Description=Platform Agent (Kog)
After=network.target openclaw-gateway.service

[Service]
Type=simple
WorkingDirectory=$AGENT_DIR
ExecStart=$BINARY
EnvironmentFile=$ENV_FILE
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF

# Enable lingering (survive logout)
loginctl enable-linger "$(whoami)" 2>/dev/null || true

# Reload and enable
systemctl --user daemon-reload
systemctl --user enable platform-agent.service

echo "✅ Service installed: platform-agent.service"
echo ""
echo "Commands:"
echo "  systemctl --user start platform-agent"
echo "  systemctl --user stop platform-agent"
echo "  systemctl --user restart platform-agent"
echo "  systemctl --user status platform-agent"
echo "  journalctl --user -u platform-agent -f"
