# Installation Guide

This guide covers installing and configuring `opencode-matrix-bot` on a Linux system as a systemd service.

## Prerequisites

- A Linux system with systemd (Ubuntu 22.04+ recommended)
- An [OpenCode](https://opencode.ai/) server running locally on port 4096
- Two Matrix accounts on any homeserver (e.g. matrix.org):
  - **Bot account** — the account the bot will log in as
  - **Owner account** — your personal account that controls the bot

## 0. Create Matrix accounts

You need two Matrix accounts. If you already have your personal account, you only need to create the bot account.

### Create the bot account on app.element.io

1. Go to [app.element.io](https://app.element.io/)
2. If you are already signed in, click your avatar (bottom left) → **Sign out**
3. Click **Create account** — keep `matrix.org` as the homeserver
4. Enter a username (e.g. `opencode-bot`), a password, and confirm via email
5. After registration, open **Settings → General → Account**
6. Note the full **Matrix ID** — it will look like `@opencode-bot:matrix.org`  
   This is your `MATRIX_USER_ID`

### Find your owner Matrix ID

1. Sign out of the bot account
2. Sign in as yourself (your personal account)
3. Open **Settings → General → Account**
4. Note your **Matrix ID** — e.g. `@yourname:matrix.org`  
   This is your `MATRIX_OWNER_ID`

### Create a Direct Message room

While signed in as yourself:

1. Click **+** next to **Direct Messages** in the left panel
2. Search for the bot by its Matrix ID (`@opencode-bot:matrix.org`)
3. Create the DM — this room is where you will send all commands to the bot

> **Note on rate limiting:** Free accounts on matrix.org have rate limiting. For personal use this is not a problem. If the bot sends many messages rapidly (e.g. from SSE streaming), you may occasionally see short delays. For heavy use, a self-hosted homeserver is recommended.

## 1. Download

Download the latest release tarball for your platform from:

```
https://github.com/rho-sk/opencode-matrix-bot/releases/latest
```

Available platforms: `linux_amd64`, `linux_arm64`

```bash
# Example for linux_amd64
VERSION=v0.1.0
curl -Lo opencode-matrix-bot.tar.gz \
  "https://github.com/rho-sk/opencode-matrix-bot/releases/download/${VERSION}/opencode-matrix-bot_${VERSION}_linux_amd64.tar.gz"
tar -xzf opencode-matrix-bot.tar.gz
cd opencode-matrix-bot_${VERSION}_linux_amd64
```

## 2. Install the binary

```bash
sudo cp opencode-matrix-bot /usr/local/bin/
sudo chmod +x /usr/local/bin/opencode-matrix-bot

# Verify
opencode-matrix-bot --version
```

## 3. Create configuration

```bash
sudo mkdir -p /etc/opencode-matrix-bot
sudo cp .env.example /etc/opencode-matrix-bot/.env
sudo chmod 600 /etc/opencode-matrix-bot/.env
sudo editor /etc/opencode-matrix-bot/.env
```

Fill in all required values:

```bash
MATRIX_HOMESERVER=https://matrix.org
MATRIX_USER_ID=@your-bot-account:matrix.org
MATRIX_PASSWORD=your_bot_password
MATRIX_OWNER_ID=@your-personal-account:matrix.org
OPENCODE_URL=http://localhost:4096
# OPENCODE_PASSWORD=   # only if OpenCode has auth enabled
```

> **Security:** The `.env` file contains credentials. Keep permissions at `600` and owned by the service user.

## 4. Create a dedicated system user

Running the bot as a dedicated user limits its privileges:

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin opencode-bot
```

If you want logs in `/var/log/opencode-matrix-bot/`, grant write access:

```bash
sudo mkdir -p /var/log/opencode-matrix-bot
sudo chown opencode-bot:opencode-bot /var/log/opencode-matrix-bot
```

Otherwise the bot will fall back to `~/.local/share/opencode-matrix-bot/logs/` under the service user's home (not available for system users without a home directory — in that case create the log dir manually).

## 5. Install the systemd service

Edit the service file to match your setup:

```bash
cp opencode-matrix-bot.service /tmp/opencode-matrix-bot.service
editor /tmp/opencode-matrix-bot.service
```

Key fields to adjust:

```ini
[Service]
User=opencode-bot
EnvironmentFile=/etc/opencode-matrix-bot/.env
ExecStart=/usr/local/bin/opencode-matrix-bot --log-level info
```

Install and start:

```bash
sudo cp /tmp/opencode-matrix-bot.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable opencode-matrix-bot
sudo systemctl start opencode-matrix-bot
```

## 6. Verify

```bash
# Check service status
sudo systemctl status opencode-matrix-bot

# Follow live logs
sudo journalctl -u opencode-matrix-bot -f

# Check log file
sudo tail -f /var/log/opencode-matrix-bot/opencode-matrix-bot.log
```

## 7. First use

1. Open Element (or any Matrix client) with your **owner account**
2. Start a direct message with your **bot account**
3. Send `/help` — the bot should respond with the command list
4. Send `/sessions` to list available OpenCode sessions
5. Send `/attach <first-8-chars-of-session-id>` to connect
6. Send any message — it will be forwarded to OpenCode as a prompt

## Updating

```bash
# Download new release, then:
sudo systemctl stop opencode-matrix-bot
sudo cp opencode-matrix-bot /usr/local/bin/
sudo systemctl start opencode-matrix-bot
sudo systemctl status opencode-matrix-bot
```

## Uninstall

```bash
sudo systemctl disable --now opencode-matrix-bot
sudo rm /etc/systemd/system/opencode-matrix-bot.service
sudo systemctl daemon-reload
sudo rm /usr/local/bin/opencode-matrix-bot
sudo rm -rf /etc/opencode-matrix-bot
sudo rm -rf /var/log/opencode-matrix-bot
sudo userdel opencode-bot
```

## Log rotation

Log rotation is built in (no logrotate needed):

- File: `/var/log/opencode-matrix-bot/opencode-matrix-bot.log`
- Max size: 5 MB per file
- Max backups: 10 (compressed with gzip)

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Bot does not respond | Wrong `MATRIX_OWNER_ID` | Check the value — must match exactly including server |
| `Matrix login failed` | Wrong credentials | Verify `MATRIX_USER_ID` and `MATRIX_PASSWORD` |
| `Connection refused` on OpenCode calls | OpenCode not running | Start OpenCode: `opencode serve` |
| `SSE stream interrupted` in logs | OpenCode restarted | Bot reconnects automatically with backoff |
| Bot responds to old messages on restart | Startup timestamp filtering | Normal behaviour — messages before start are ignored |
