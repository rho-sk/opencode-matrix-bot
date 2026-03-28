package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/lumberjack.v2"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Build-time variables injected by ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

const (
	logMaxSizeMB  = 5
	logMaxBackups = 10
	logDir        = "/var/log/opencode-matrix-bot"
	logFile       = "opencode-matrix-bot.log"
)

func setupLogging(levelStr string) {
	// Parse log level
	level, err := zerolog.ParseLevel(levelStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid log level %q, defaulting to info\n", levelStr)
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Determine log directory: prefer /var/log/<app>, fall back to ~/.local/share/<app>/logs
	dir := logDir
	if err := os.MkdirAll(dir, 0o750); err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share", "opencode-matrix-bot", "logs")
		if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
			// Last resort: only console
			log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
			log.Warn().Str("reason", mkErr.Error()).Msg("Could not create log directory, logging to stderr only")
			return
		}
	}

	rotatingFile := &lumberjack.Logger{
		Filename:   filepath.Join(dir, logFile),
		MaxSize:    logMaxSizeMB, // MB
		MaxBackups: logMaxBackups,
		Compress:   true,
	}

	// Write structured JSON to file, human-readable to stderr
	console := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	multi := io.MultiWriter(console, rotatingFile)
	log.Logger = zerolog.New(multi).With().Timestamp().Logger()

	log.Info().
		Str("log_dir", dir).
		Str("log_level", level.String()).
		Int("max_size_mb", logMaxSizeMB).
		Int("max_backups", logMaxBackups).
		Msg("Logging initialized")
}

func main() {
	// CLI flags
	logLevel := flag.String("log-level", "info", "Log level: trace, debug, info, warn, error, fatal")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("opencode-matrix-bot %s (commit %s, built %s)\n", version, commit, buildDate)
		os.Exit(0)
	}

	setupLogging(*logLevel)

	// Load configuration
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Configuration error")
	}

	log.Info().
		Str("version", version).
		Str("homeserver", cfg.MatrixHomeserver).
		Str("user", cfg.MatrixUserID).
		Str("opencode", cfg.OpencodeURL).
		Msg("Starting opencode-matrix-bot")

	// Record startup timestamp (milliseconds) to ignore historical messages
	startupTS := time.Now().UnixMilli()

	// Create Matrix client (not yet logged in — CryptoHelper handles login)
	matrixClient, err := mautrix.NewClient(cfg.MatrixHomeserver, id.UserID(cfg.MatrixUserID), "")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Matrix client")
	}

	ctx := context.Background()

	// Create Opencode client
	oc := NewOpencodeClient(cfg)

	// Create bot
	bot := NewBot(cfg, matrixClient, oc, startupTS)

	// Set up syncer
	syncer := mautrix.NewDefaultSyncer()

	// Auto-accept invites from the owner only
	syncer.OnEventType(event.StateMember, func(ctx context.Context, evt *event.Event) {
		if evt.GetStateKey() != cfg.MatrixUserID {
			return
		}
		content, ok := evt.Content.Parsed.(*event.MemberEventContent)
		if !ok || content.Membership != event.MembershipInvite {
			return
		}
		if string(evt.Sender) != cfg.MatrixOwnerID {
			log.Warn().Str("sender", string(evt.Sender)).Msg("Ignoring invite from non-owner")
			return
		}
		log.Info().Str("room", string(evt.RoomID)).Msg("Accepting invite from owner")
		if _, err := matrixClient.JoinRoomByID(ctx, evt.RoomID); err != nil {
			log.Error().Err(err).Str("room", string(evt.RoomID)).Msg("Failed to join room")
		}
	})

	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		bot.HandleMessage(ctx, evt)
	})
	matrixClient.Syncer = syncer

	// Set up E2E crypto — CryptoHelper also handles login and reuses device ID from DB
	cryptoHelper, err := cryptohelper.NewCryptoHelper(matrixClient, []byte(cfg.PickleKey), cfg.CryptoDBPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create crypto helper")
	}
	cryptoHelper.LoginAs = &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: cfg.MatrixUserID},
		Password:                 cfg.MatrixPassword,
		InitialDeviceDisplayName: "opencode-matrix-bot",
	}
	if err := cryptoHelper.Init(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to init crypto helper")
	}
	defer cryptoHelper.Close()
	matrixClient.Crypto = cryptoHelper
	log.Info().
		Str("db", cfg.CryptoDBPath).
		Str("device_id", string(matrixClient.DeviceID)).
		Msg("E2E crypto initialized, login successful")

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	syncCtx, syncCancel := context.WithCancel(ctx)
	defer syncCancel()

	go func() {
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("Received signal, shutting down")
		syncCancel()
	}()

	log.Info().Msg("Starting Matrix sync loop")
	if err := matrixClient.SyncWithContext(syncCtx); err != nil && syncCtx.Err() == nil {
		log.Fatal().Err(err).Msg("Matrix sync failed")
	}

	log.Info().Msg("Bot stopped")
}
