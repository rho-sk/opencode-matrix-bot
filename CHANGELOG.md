# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).  
This project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).  
The changelog is auto-generated from [Conventional Commits](https://www.conventionalcommits.org/) using [git-cliff](https://git-cliff.org/).

<!-- next-release -->

## [0.1.0] - 2026-03-27

### Added

- Matrix bot that bridges Element mobile client to a local OpenCode server
- Commands: `/help`, `/sessions`, `/attach`, `/detach`, `/status`, `/todo`, `/abort`, `/new`
- Real-time SSE streaming: tool invocations and AI text delivered as Matrix messages
- Rotating log files (5 MB × 10 backups, gzip compressed)
- `--log-level` CLI flag (trace/debug/info/warn/error/fatal)
- `--version` CLI flag with build-time version, commit, and date injection
- systemd service unit (`deploy/opencode-matrix-bot.service`)
- Unit tests for config, commands, SSE parsing, and debounce logic
- Installation guide (`docs/installation.md`)
- Cross-platform release builds for `linux/amd64` and `linux/arm64`
