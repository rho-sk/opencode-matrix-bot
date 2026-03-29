# opencode-matrix-bot

A Matrix bot that bridges your mobile [Element](https://element.io/) client to a locally running [OpenCode](https://opencode.ai/) AI coding agent.

```
Mobile (Element)
      ↕ HTTPS
  matrix.org  (bot + user accounts)
      ↕ HTTPS  (long-poll sync)
  opencode-matrix-bot  (this program, runs on your PC)
      ↕ HTTP
  OpenCode server  (localhost:4096)
```

The bot connects **outward** to matrix.org — no open ports or tunnels required.

## Features

### Session Management
- Send messages to an OpenCode session from any Matrix client
- List, attach, detach, and create sessions
- Real-time streaming via SSE: tool invocations and AI responses delivered as Matrix messages
- `/todo` — view the active TODO list
- `/abort` — interrupt a running session

### Permissions Handling
- Interactive permission requests from OpenCode
- `/allow-once` — grant permission for single use
- `/allow-always` — grant permission permanently
- `/deny` — reject permission request

### Questions & Interactive Input
- Progressive multi-question sequences with "Question X/Y" indicator
- Step-by-step answering via `/answer <text or number>`
- Support for both predefined options and custom text answers
- `/reject` — skip current question
- `/reject_all` — cancel entire question sequence
- All answers accumulated and submitted together upon completion

### Context Tracking
- Real-time token usage tracking (input, output, cache)
- `/context` — display accumulated tokens and cost for session
- Cost tracking ready for providers that support it
- Formatted with thousand separators for readability

### Other Features
- Rotating log files (5 MB × 10 backups) with configurable log level
- Single static binary, runs as a systemd service

## Matrix account setup

You need **two Matrix accounts** on [app.element.io](https://app.element.io/) (or any homeserver):

- **Bot account** — the account the bot logs in as (e.g. `@opencode-bot:matrix.org`)
- **Owner account** — your personal account that sends commands

### Create the bot account

1. Go to [app.element.io](https://app.element.io/) and sign out of your current account
2. Click **Create account** → keep `matrix.org` as the server
3. Choose a username (e.g. `opencode-bot`), set a password, confirm via email
4. After registration, go to **Settings → General → Account** — note the full Matrix ID (e.g. `@opencode-bot:matrix.org`) — this is `MATRIX_USER_ID`

### Find your owner Matrix ID

Sign back in as yourself → **Settings → General → Account** → note your Matrix ID (e.g. `@yourname:matrix.org`) — this is `MATRIX_OWNER_ID`

### Create a Direct Message room

While logged in as yourself, click **+** next to **Direct Messages** in the left panel, search for the bot account by its Matrix ID, and create the DM. This is the room you will use to control the bot.

> **Note:** matrix.org free accounts have rate limiting. For heavy SSE streaming use, a self-hosted homeserver is recommended, but for personal use matrix.org works fine.

---

## Quick start

1. **Download** the latest release tarball for your platform from the [Releases](https://github.com/rho-sk/opencode-matrix-bot/releases) page.

2. **Extract and install:**

   ```bash
   tar -xzf opencode-matrix-bot_<version>_linux_amd64.tar.gz
   cd opencode-matrix-bot_<version>_linux_amd64
   sudo cp opencode-matrix-bot /usr/local/bin/
   ```

3. **Configure** — copy `.env.example` to `/etc/opencode-matrix-bot/.env` (or the working directory) and fill in your credentials:

   ```bash
   sudo mkdir /etc/opencode-matrix-bot
   sudo cp .env.example /etc/opencode-matrix-bot/.env
   sudo editor /etc/opencode-matrix-bot/.env
   ```

4. **Install and start the systemd service:**

   ```bash
   sudo cp opencode-matrix-bot.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now opencode-matrix-bot
   ```

See [docs/installation.md](docs/installation.md) for the full installation guide.

## Configuration

All configuration is read from environment variables or a `.env` file in the working directory.

| Variable | Required | Description | Example |
|---|---|---|---|
| `MATRIX_HOMESERVER` | yes | Matrix server URL | `https://matrix.org` |
| `MATRIX_USER_ID` | yes | Bot's Matrix user ID | `@mybot:matrix.org` |
| `MATRIX_PASSWORD` | yes | Bot's password | `secret` |
| `MATRIX_OWNER_ID` | yes | Only this user can control the bot | `@you:matrix.org` |
| `OPENCODE_URL` | no | OpenCode server URL | `http://localhost:4096` |
| `OPENCODE_PASSWORD` | no | HTTP Basic Auth password | *(empty)* |

## Commands

### Session Management
| Command | Description |
|---|---|
| `/help` | Show all commands |
| `/sessions` | List all OpenCode sessions with status |
| `/attach <ID>` | Attach to a session (first 8 chars of ID are enough) |
| `/detach` | Detach from current session |
| `/status` | Show status of attached session |
| `/context` | Show token usage and cost for current session |
| `/todo` | Show TODO list of attached session |
| `/abort` | Abort the running session |
| `/new [title]` | Create a new session and attach to it |

### Permissions
| Command | Description |
|---|---|
| `/allow-once` | Grant permission for single use |
| `/allow-always` | Grant permission permanently |
| `/deny` | Reject permission request |

### Questions (Step-by-step)
| Command | Description |
|---|---|
| `/answer <text\|number>` | Answer current question with text or option number |
| `/reject` | Skip current question (submit empty answer) |
| `/reject_all` | Cancel entire question sequence |

### General
| *(any other text)* | Send as a prompt to the attached session |

## Usage

### Running the bot

```bash
# Run with default log level (info)
opencode-matrix-bot

# Enable debug logging
opencode-matrix-bot --log-level debug

# Print version
opencode-matrix-bot --version
```

Logs are written to `/var/log/opencode-matrix-bot/opencode-matrix-bot.log`  
(fallback: `~/.local/share/opencode-matrix-bot/logs/`) and also to stderr.

### Example Workflow

```
You: /attach abc12345
Bot: ✅ Attached to session abc12345 (title: "My Project")

You: Create a setup wizard that asks 3 questions
Bot: 🔧 `file_write`
Bot: ⏳ Processing...
Bot: ❓ Question 1/3
     What is your name?
     Respond with: /answer <text>

You: /answer Alice
Bot: ❓ Question 2/3
     Choose framework: 
     1. FastAPI
     2. Django
     3. Flask
     Respond with: /answer <number>

You: /answer 1
Bot: ❓ Question 3/3
     Use PostgreSQL?
     1. Yes
     2. No
     Respond with: /answer <number>

You: /answer 1
Bot: ✅ All questions answered: 3 answers submitted
Bot: Based on your answers: Alice, FastAPI, PostgreSQL...

You: /status
Bot: 🟢 running
```

### Permission Request Example

```
Bot: 🔐 Permission Request
     Permission: file.read
     Patterns: src/**/*.ts
     Respond with:
     - /allow-once — Grant for this operation
     - /allow-always — Grant permanently
     - /deny — Reject

You: /allow-once
Bot: ✅ Permission: file.read (once)
```

### Context Usage Example

```
You: /context
Bot: 📊 **Context Usage**

**Tokens:**
  ↑ Input: 1,234
  ↓ Output: 5,678
  💾 Cache read: 456
  **Total: 7,368**

💰 **Cost:** $0.0234
```

Note: Cost is only displayed if your AI provider includes cost information in responses.

## Building from source

```bash
git clone https://github.com/rho-sk/opencode-matrix-bot
cd opencode-matrix-bot
make build          # produces ./opencode-matrix-bot
make test           # run unit tests
make check          # fmt + vet + test
make dist           # cross-compile release tarballs into dist/
```

Go 1.22+ is required.

## Project structure

```
opencode-matrix-bot/
├── main.go                    # Entry point, logging setup, Matrix sync loop
├── config.go                  # Configuration loading from env / .env
├── bot.go                     # Message handling, command dispatch, room state
├── opencode.go                # OpenCode HTTP API client (SDK + direct HTTP)
├── sse.go                     # SSE stream listener goroutine
├── bot_test.go                # Unit tests
├── Makefile
├── go.mod / go.sum
├── cliff.toml                 # git-cliff changelog config
├── .env.example               # Configuration template
├── deploy/
│   └── opencode-matrix-bot.service   # systemd unit
├── deploy.local/              # Local dev run (gitignored .env + run.sh)
│   ├── .env.example
│   └── run.sh
└── docs/
    └── installation.md        # Full installation guide
```

## License

MIT
