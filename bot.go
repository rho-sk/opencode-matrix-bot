package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// RoomState holds per-room state: which session is attached and the SSE cancel func.
type RoomState struct {
	AttachedSessionID string
	SSECancel         context.CancelFunc
	CurrentPermission PermissionRequest // Latest pending permission
	CurrentQuestion   QuestionRequest   // Latest pending question
}

// Bot holds all runtime state for the Matrix bot.
type Bot struct {
	cfg        *Config
	client     *mautrix.Client
	opencode   *OpencodeClient
	roomStates map[id.RoomID]*RoomState
	mu         sync.Mutex
	// startupTS is the timestamp (ms) at startup; messages older than this are ignored.
	startupTS int64
	// sendMsgFn overrides the default Matrix send; used in tests.
	sendMsgFn func(roomID id.RoomID, text string)
}

// NewBot creates a new Bot instance.
func NewBot(cfg *Config, matrixClient *mautrix.Client, oc *OpencodeClient, startupTS int64) *Bot {
	return &Bot{
		cfg:        cfg,
		client:     matrixClient,
		opencode:   oc,
		roomStates: map[id.RoomID]*RoomState{},
		startupTS:  startupTS,
	}
}

// HandleMessage is the mautrix event handler for m.room.message events.
func (b *Bot) HandleMessage(ctx context.Context, evt *event.Event) {
	log.Debug().
		Str("sender", string(evt.Sender)).
		Str("room", string(evt.RoomID)).
		Int64("ts", evt.Timestamp).
		Int64("startupTS", b.startupTS).
		Msg("Received message event")

	// Ignore own messages
	if string(evt.Sender) == b.cfg.MatrixUserID {
		log.Debug().Msg("Ignoring own message")
		return
	}
	// Ignore messages from non-owners
	if string(evt.Sender) != b.cfg.MatrixOwnerID {
		log.Debug().Str("sender", string(evt.Sender)).Msg("Ignoring message from non-owner")
		return
	}
	// Ignore messages older than startup time
	if evt.Timestamp < b.startupTS {
		log.Debug().Int64("ts", evt.Timestamp).Msg("Ignoring old message")
		return
	}

	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok {
		log.Debug().Msg("Failed to parse message content")
		return
	}
	if content.MsgType != event.MsgText {
		return
	}

	text := strings.TrimSpace(content.Body)
	// Run in goroutine to avoid blocking the Matrix sync loop
	go b.handleCommand(ctx, evt.RoomID, text)
}

// handleCommand dispatches a message text to the appropriate command handler.
func (b *Bot) handleCommand(ctx context.Context, roomID id.RoomID, text string) {
	switch {
	case text == "/help":
		b.cmdHelp(ctx, roomID)

	case text == "/sessions":
		b.cmdSessions(ctx, roomID)

	case strings.HasPrefix(text, "/attach"):
		parts := strings.Fields(text)
		if len(parts) < 2 {
			b.sendMsg(roomID, "Použitie: /attach <ID>")
			return
		}
		b.cmdAttach(ctx, roomID, parts[1])

	case text == "/detach":
		b.cmdDetach(ctx, roomID)

	case text == "/status":
		b.cmdStatus(ctx, roomID)

	case text == "/todo":
		b.cmdTodo(ctx, roomID)

	case text == "/abort":
		b.cmdAbort(ctx, roomID)

	case strings.HasPrefix(text, "/new"):
		parts := strings.SplitN(text, " ", 2)
		title := ""
		if len(parts) == 2 {
			title = strings.TrimSpace(parts[1])
		}
		b.cmdNew(ctx, roomID, title)

	case text == "/allow-once":
		b.cmdAllowOnce(ctx, roomID)

	case text == "/allow-always":
		b.cmdAllowAlways(ctx, roomID)

	case text == "/deny":
		b.cmdDeny(ctx, roomID)

	case strings.HasPrefix(text, "/answer"):
		parts := strings.Fields(text)
		if len(parts) < 2 {
			b.sendMsg(roomID, "Použitie: /answer <answers...>")
			return
		}
		b.cmdAnswer(ctx, roomID, parts[1:])

	case text == "/dismiss-question":
		b.cmdDismissQuestion(ctx, roomID)

	case strings.HasPrefix(text, "/"):
		b.sendMsg(roomID, fmt.Sprintf("Neznámy príkaz: %s\nPoužite /help pre zoznam príkazov.", text))

	default:
		b.cmdPrompt(ctx, roomID, text)
	}
}

// cmdHelp sends the help message.
func (b *Bot) cmdHelp(ctx context.Context, roomID id.RoomID) {
	help := `Dostupné príkazy:
/help — tento zoznam
/sessions — zoznam všetkých sessions
/attach <ID> — pripoj sa na session (stačí prvých 8 znakov ID)
/detach — odpoj sa od aktuálnej session
/status — stav pripojenej session
/todo — TODO zoznam pripojenej session
/abort — prerušenie bežiacej session
/new [názov] — vytvorenie novej session

Akákoľvek iná správa sa odošle do pripojenej session.`
	b.sendMsg(roomID, help)
}

// cmdSessions lists all sessions with their status.
func (b *Bot) cmdSessions(ctx context.Context, roomID id.RoomID) {
	sessions, err := b.opencode.ListSessions(ctx)
	if err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba pri načítaní sessions: %s", err))
		return
	}
	if len(sessions) == 0 {
		b.sendMsg(roomID, "Žiadne sessions.")
		return
	}

	// Get status map
	statusMap, err := b.opencode.GetSessionStatus(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get session status")
		statusMap = map[string]SessionStatus{}
	}

	var sb strings.Builder
	for _, s := range sessions {
		icon := stateIcon(statusMap[s.ID].State)
		title := s.Title
		if title == "" {
			title = "(bez názvu)"
		}
		shortID := s.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		sb.WriteString(fmt.Sprintf("%s %s — %s\n", icon, shortID, title))
	}
	b.sendMsg(roomID, strings.TrimRight(sb.String(), "\n"))
}

// cmdAttach attaches the room to a session by ID prefix.
func (b *Bot) cmdAttach(ctx context.Context, roomID id.RoomID, prefix string) {
	sessions, err := b.opencode.ListSessions(ctx)
	if err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba: %s", err))
		return
	}

	var matched *struct{ id, title string }
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, prefix) {
			matched = &struct{ id, title string }{s.ID, s.Title}
			break
		}
	}
	if matched == nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Session s prefixom '%s' nenájdená.", prefix))
		return
	}

	b.mu.Lock()
	state := b.getOrCreateRoomState(roomID)
	// Cancel existing SSE listener if any
	if state.SSECancel != nil {
		state.SSECancel()
		state.SSECancel = nil
	}
	state.AttachedSessionID = matched.id
	b.mu.Unlock()

	title := matched.title
	if title == "" {
		title = "(bez názvu)"
	}
	b.sendMsg(roomID, fmt.Sprintf("🔗 Pripojený na session `%s` — %s", prefix, title))

	// Get last message ID before attaching — used to skip historical SSE events
	lastMsgID, err := b.opencode.GetLastMessageID(ctx, matched.id)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get last message ID, SSE history will not be skipped")
	}
	log.Debug().Str("lastMsgID", lastMsgID).Msg("Attaching SSE listener")

	// Start SSE listener
	cancel := StartSSEListener(ctx, b.cfg, b.opencode, matched.id, roomID, b.sendMsg, lastMsgID,
		func(typing bool) {
			b.sendTyping(roomID, typing)
		},
		func(permReq PermissionRequest) {
			b.handlePermissionRequest(ctx, roomID, permReq)
		},
		func(qReq QuestionRequest) {
			b.handleQuestionRequest(ctx, roomID, qReq)
		})
	b.mu.Lock()
	state.SSECancel = cancel
	b.mu.Unlock()
}

// cmdDetach detaches the room from its current session.
func (b *Bot) cmdDetach(ctx context.Context, roomID id.RoomID) {
	b.mu.Lock()
	state := b.getOrCreateRoomState(roomID)
	if state.SSECancel != nil {
		state.SSECancel()
		state.SSECancel = nil
	}
	state.AttachedSessionID = ""
	b.mu.Unlock()
	b.sendMsg(roomID, "🔌 Odpojený")
}

// cmdStatus shows the status of the attached session.
func (b *Bot) cmdStatus(ctx context.Context, roomID id.RoomID) {
	sessionID := b.attachedSessionID(roomID)
	if sessionID == "" {
		b.sendMsg(roomID, "❌ Nie si pripojený k žiadnej session.")
		return
	}
	statusMap, err := b.opencode.GetSessionStatus(ctx)
	if err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba: %s", err))
		return
	}
	st := statusMap[sessionID]
	icon := stateIcon(st.State)
	state := st.State
	if state == "" {
		state = "neznámy"
	}
	b.sendMsg(roomID, fmt.Sprintf("%s %s", icon, state))
}

// cmdTodo shows the TODO list for the attached session.
func (b *Bot) cmdTodo(ctx context.Context, roomID id.RoomID) {
	sessionID := b.attachedSessionID(roomID)
	if sessionID == "" {
		b.sendMsg(roomID, "❌ Nie si pripojený k žiadnej session.")
		return
	}
	items, err := b.opencode.GetTodo(ctx, sessionID)
	if err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba: %s", err))
		return
	}
	if len(items) == 0 {
		b.sendMsg(roomID, "TODO zoznam je prázdny.")
		return
	}
	var sb strings.Builder
	for _, item := range items {
		check := "⬜"
		if item.Completed {
			check = "✅"
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", check, item.Content))
	}
	b.sendMsg(roomID, strings.TrimRight(sb.String(), "\n"))
}

// cmdAbort aborts the attached session.
func (b *Bot) cmdAbort(ctx context.Context, roomID id.RoomID) {
	sessionID := b.attachedSessionID(roomID)
	if sessionID == "" {
		b.sendMsg(roomID, "❌ Nie si pripojený k žiadnej session.")
		return
	}
	if err := b.opencode.AbortSession(ctx, sessionID); err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba pri prerušení: %s", err))
		return
	}
	b.sendMsg(roomID, "🛑 Prerušené")
}

// cmdNew creates a new session and attaches to it.
func (b *Bot) cmdNew(ctx context.Context, roomID id.RoomID, title string) {
	session, err := b.opencode.NewSession(ctx, title)
	if err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba pri vytváraní session: %s", err))
		return
	}
	// Use the first 8 chars as prefix to attach
	prefix := session.ID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	b.cmdAttach(ctx, roomID, prefix)
}

// cmdAllowOnce grants the current pending permission once.
func (b *Bot) cmdAllowOnce(ctx context.Context, roomID id.RoomID) {
	b.respondToPermission(ctx, roomID, "once")
}

// cmdAllowAlways grants the current pending permission always.
func (b *Bot) cmdAllowAlways(ctx context.Context, roomID id.RoomID) {
	b.respondToPermission(ctx, roomID, "always")
}

// cmdDeny denies the current pending permission.
func (b *Bot) cmdDeny(ctx context.Context, roomID id.RoomID) {
	b.respondToPermission(ctx, roomID, "reject")
}

// respondToPermission is a helper that responds to the current pending permission.
func (b *Bot) respondToPermission(ctx context.Context, roomID id.RoomID, response string) {
	b.mu.Lock()
	state := b.getOrCreateRoomState(roomID)
	perm := state.CurrentPermission
	state.CurrentPermission = PermissionRequest{} // Clear current permission
	b.mu.Unlock()

	if perm.ID == "" {
		b.sendMsg(roomID, "❌ No pending permission to respond to.")
		return
	}

	if err := b.opencode.RespondToPermission(ctx, perm.ID, response); err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba pri odpoveди na permission: %s", err))
		return
	}

	b.sendMsg(roomID, fmt.Sprintf("✅ Permission: %s (%s)", perm.Type, response))
}

// cmdAnswer answers the current pending question.
func (b *Bot) cmdAnswer(ctx context.Context, roomID id.RoomID, answers []string) {
	b.mu.Lock()
	state := b.getOrCreateRoomState(roomID)
	qReq := state.CurrentQuestion
	state.CurrentQuestion = QuestionRequest{} // Clear current question
	b.mu.Unlock()

	if qReq.ID == "" {
		b.sendMsg(roomID, "❌ No pending question to answer.")
		return
	}

	// Convert string answers to [][]string format
	answersList := [][]string{answers}

	if err := b.opencode.RespondToQuestion(ctx, qReq.ID, answersList); err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba pri odpoveди na otázku: %s", err))
		return
	}

	b.sendMsg(roomID, fmt.Sprintf("✅ Question answered: %d answers submitted", len(answers)))
}

// cmdDismissQuestion dismisses the current pending question.
func (b *Bot) cmdDismissQuestion(ctx context.Context, roomID id.RoomID) {
	b.mu.Lock()
	state := b.getOrCreateRoomState(roomID)
	qReq := state.CurrentQuestion
	state.CurrentQuestion = QuestionRequest{} // Clear current question
	b.mu.Unlock()

	if qReq.ID == "" {
		b.sendMsg(roomID, "❌ No pending question to dismiss.")
		return
	}

	if err := b.opencode.RejectQuestion(ctx, qReq.ID); err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba pri odmietnutí otázky: %s", err))
		return
	}

	b.sendMsg(roomID, "✅ Question dismissed")
}

// cmdPrompt sends a free-text message to the attached session.
func (b *Bot) cmdPrompt(ctx context.Context, roomID id.RoomID, text string) {
	sessionID := b.attachedSessionID(roomID)
	if sessionID == "" {
		b.sendMsg(roomID, "Nie si pripojený k žiadnej session. Použi /sessions a /attach <ID>")
		return
	}
	if err := b.opencode.PromptAsync(ctx, sessionID, text); err != nil {
		b.sendMsg(roomID, fmt.Sprintf("❌ Chyba pri odosielaní: %s", err))
	}
}

// sendMsg sends a plain text message to a Matrix room.
func (b *Bot) sendMsg(roomID id.RoomID, text string) {
	if b.sendMsgFn != nil {
		b.sendMsgFn(roomID, text)
		return
	}
	_, err := b.client.SendMessageEvent(context.Background(), roomID, event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    text,
	})
	if err != nil {
		log.Error().Err(err).Str("room", string(roomID)).Msg("Failed to send message")
	}
}

// sendTyping sends or clears the typing indicator for the bot in a room.
// timeout is how long the indicator stays active (ignored when typing=false).
func (b *Bot) sendTyping(roomID id.RoomID, typing bool) {
	if b.client == nil {
		return
	}
	timeout := 30 * time.Second
	if _, err := b.client.UserTyping(context.Background(), roomID, typing, timeout); err != nil {
		log.Debug().Err(err).Msg("Failed to send typing indicator")
	}
}

// sendMsgFromHistory formats and sends a historical message (best-effort text extraction).
func (b *Bot) sendMsgFromHistory(roomID id.RoomID, msg interface{}) {
	_ = msg
	// Historical messages use complex SDK union types.
	// Text extraction is handled via SSE going forward; history is shown as a note.
}

// attachedSessionID returns the session ID attached to this room (empty if none).
func (b *Bot) attachedSessionID(roomID id.RoomID) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.getOrCreateRoomState(roomID)
	return state.AttachedSessionID
}

// getOrCreateRoomState returns (and creates if necessary) the RoomState for a room.
// Caller must hold b.mu.
func (b *Bot) getOrCreateRoomState(roomID id.RoomID) *RoomState {
	if b.roomStates[roomID] == nil {
		b.roomStates[roomID] = &RoomState{}
	}
	return b.roomStates[roomID]
}

// handlePermissionRequest handles a permission request from OpenCode
func (b *Bot) handlePermissionRequest(ctx context.Context, roomID id.RoomID, permReq PermissionRequest) {
	// Build description from metadata or patterns
	description := fmt.Sprintf("Permission requested: %s\n", permReq.Type)
	if patterns := permReq.Patterns; len(patterns) > 0 {
		for _, p := range patterns {
			description += fmt.Sprintf("  • %s\n", p)
		}
	}

	// Send permission dialog to user - NO ID REQUIRED
	msg := fmt.Sprintf(
		"❓ **Permission required**\n%s\n"+
			"Respond with:\n"+
			"- `/allow-once` — Grant once\n"+
			"- `/allow-always` — Grant always\n"+
			"- `/deny` — Reject",
		description)
	b.sendMsg(roomID, msg)

	// Store as current permission for later response
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.getOrCreateRoomState(roomID)
	state.CurrentPermission = permReq
}

// handleQuestionRequest handles a question request from OpenCode
func (b *Bot) handleQuestionRequest(ctx context.Context, roomID id.RoomID, qReq QuestionRequest) {
	// Build question message
	msg := "❓ **Question**\n\n"

	for i, qMap := range qReq.Questions {
		if question, ok := qMap["question"].(string); ok {
			msg += fmt.Sprintf("**Q%d.** %s\n", i+1, question)

			// Add options if available
			if options, ok := qMap["options"].([]interface{}); ok {
				for _, opt := range options {
					if optMap, ok := opt.(map[string]interface{}); ok {
						if label, ok := optMap["label"].(string); ok {
							msg += fmt.Sprintf("  • %s\n", label)
						}
					}
				}
			}
			msg += "\n"
		}
	}

	msg += "Respond with:\n- `/answer <your answers>` — Submit answers\n- `/dismiss-question` — Dismiss"
	b.sendMsg(roomID, msg)

	// Store as current question for later response
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.getOrCreateRoomState(roomID)
	state.CurrentQuestion = qReq
}

// stateIcon returns an emoji icon for a session state string.
func stateIcon(state string) string {
	switch state {
	case "running", "busy":
		return "🟢"
	case "idle":
		return "⚪"
	case "error":
		return "🔴"
	case "retry":
		return "⏳"
	default:
		return "❓"
	}
}
