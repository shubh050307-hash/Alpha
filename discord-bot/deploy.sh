#!/bin/bash
# Deploy Alpha Discord Bot to the VPS
# Run from the VPS host after pulling from GitHub

set -e

echo "⚡ Deploying Alpha Network Discord Bot..."

# Create directory
mkdir -p /opt/Alpha-Network/discord-bot

# Copy files
cp /opt/Alpha-Network/discord-bot/bot.py /opt/Alpha-Network/discord-bot/bot.py 2>/dev/null || true
cp /opt/Alpha-Network/discord-bot/milestone-state.json /opt/Alpha-Network/discord-bot/milestone-state.json 2>/dev/null || true
cp /opt/Alpha-Network/discord-bot/alpha-discord-bot.service /etc/systemd/system/alpha-discord-bot.service

# Install Python dependency
pip3 install requests 2>/dev/null || pip install requests

# Create .env if it doesn't exist (fill in your values!)
if [ ! -f /opt/Alpha-Network/discord-bot/.env ]; then
    echo "⚠️  Creating .env template — FILL IN YOUR TOKEN AND CHANNEL ID:"
    cat > /opt/Alpha-Network/discord-bot/.env << 'EOF'
DISCORD_BOT_TOKEN=YOUR_BOT_TOKEN_HERE
DISCORD_GENERAL_CHANNEL_ID=YOUR_CHANNEL_ID_HERE
EOF
    echo "   Edit /opt/Alpha-Network/discord-bot/.env with real values, then re-run this script."
    exit 1
fi

# Restart service
systemctl daemon-reload
systemctl enable alpha-discord-bot
systemctl restart alpha-discord-bot

echo "✅ Discord bot deployed!"
echo "   Check status: systemctl status alpha-discord-bot"
echo "   View logs:    journalctl -u alpha-discord-bot -f"
