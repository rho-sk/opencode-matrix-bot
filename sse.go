package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	opencode "github.com/sst/opencode-sdk-go"
	"maunium.net/go/mautrix/id"
)

// StartSSEListener starts a goroutine that reads the OpenCode SSE stream using
// the official SDK and forwards relevant events to the Matrix room.
//
// typingFn: called with true when processing starts, false when done.
// onPermission: called when a permission request arrives (sessionID, Permission).
func StartSSEListener(
	ctx context.Context,
	cfg *Config,
	oc *OpencodeClient,
	attachedID string,
	roomID id.RoomID,
	sendMsg func(id.RoomID, string),
	lastMsgID string,
	typingFn func(bool),
	onPermission func(string, *opencode.Permission),
) context.CancelFunc {
	childCtx, cancel := context.WithCancel(ctx)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("recover", r).Msg("SSE goroutine panic recovered")
			}
		}()

		backoff := 2 * time.Second
		for {
			select {
			case <-childCtx.Done():
				return
			default:
			}

			err := runSDKSSELoop(childCtx, oc, attachedID, roomID, sendMsg, lastMsgID, typingFn, onPermission)
			if err != nil {
				select {
				case <-childCtx.Done():
					return
				default:
				}
				log.Warn().Err(err).Str("sessionID", attachedID).Msg("SSE stream error, reconnecting")
				sendMsg(roomID, fmt.Sprintf("⚠️ SSE stream prerušený: %s", err.Error()))
			} else {
				select {
				case <-childCtx.Done():
					return
				default:
				}
				log.Info().Str("sessionID", attachedID).Msg("SSE stream closed, reconnecting")
			}
			select {
			case <-childCtx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			// Reset backoff on successful reconnect attempt
			backoff = 2 * time.Second
		}
	}()

	return cancel
}

// trackedSessions is a thread-safe set of session IDs being monitored.
type trackedSessions struct {
	mu  sync.RWMutex
	ids map[string]bool
	// childOf maps childID -> parentID (for logging)
	childOf map[string]string
}

func newTrackedSessions(rootID string) *trackedSessions {
	return &trackedSessions{
		ids:     map[string]bool{rootID: true},
		childOf: map[string]string{},
	}
}

func (t *trackedSessions) add(id, parentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ids[id] = true
	t.childOf[id] = parentID
}

func (t *trackedSessions) contains(id string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ids[id]
}

func (t *trackedSessions) isChild(id string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.childOf[id]
	return ok
}

// runSDKSSELoop uses the SDK Event.ListStreaming to process events.
// It returns nil when the stream closes cleanly (caller should reconnect),
// or an error on failure.
func runSDKSSELoop(
	ctx context.Context,
	oc *OpencodeClient,
	attachedID string,
	roomID id.RoomID,
	sendMsg func(id.RoomID, string),
	lastMsgID string,
	typingFn func(bool),
	onPermission func(string, *opencode.Permission),
) error {
	// Wrap context with a heartbeat timeout — if no event arrives for 90s, reconnect
	const heartbeatTimeout = 90 * time.Second
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()

	// Watchdog: reset a timer on each received event; cancel if it fires
	watchdog := time.AfterFunc(heartbeatTimeout, func() {
		log.Warn().Str("sessionID", attachedID).Msg("SSE heartbeat timeout, reconnecting")
		hbCancel()
	})
	defer watchdog.Stop()
	resetWatchdog := func() { watchdog.Reset(heartbeatTimeout) }

	stream := oc.sdk.Event.ListStreaming(hbCtx, opencode.EventListParams{})
	defer stream.Close()

	tracked := newTrackedSessions(attachedID)

	// deltaAccum accumulates streaming text per partID for debouncing
	deltaAccum := map[string]string{}
	deltaLastSent := map[string]int{}
	// announcedTools prevents duplicate 🔧 messages per part
	announcedTools := map[string]bool{}
	// assistantMessages tracks messageIDs confirmed as assistant messages
	// (identified by seeing a step-start part for that messageID)
	assistantMessages := map[string]bool{}

	// typingActive tracks whether we currently have typing indicator on
	typingActive := false
	var typingTicker *time.Ticker
	startTyping := func() {
		if !typingActive {
			typingActive = true
			typingFn(true)
		}
		if typingTicker == nil {
			typingTicker = time.NewTicker(15 * time.Second)
			go func() {
				for range typingTicker.C {
					typingFn(true)
				}
			}()
		}
	}
	stopTyping := func() {
		if typingTicker != nil {
			typingTicker.Stop()
			typingTicker = nil
		}
		if typingActive {
			typingActive = false
			typingFn(false)
		}
	}
	defer stopTyping()

	for stream.Next() {
		resetWatchdog()
		evt := stream.Current()

		switch evt.Type {

		case opencode.EventListResponseTypeMessagePartUpdated:
			props, ok := evt.Properties.(opencode.EventListResponseEventMessagePartUpdatedProperties)
			if !ok {
				continue
			}
			part := props.Part

			// Skip historical events
			if lastMsgID != "" && part.MessageID != "" && part.MessageID <= lastMsgID {
				continue
			}
			if !tracked.contains(part.SessionID) {
				continue
			}

			prefix := ""
			if tracked.isChild(part.SessionID) {
				prefix = "↳ "
			}

			switch part.Type {

			case opencode.PartTypeStepStart:
				// Mark this messageID as an assistant message and show typing indicator
				assistantMessages[part.MessageID] = true
				startTyping()

			case opencode.PartTypeText:
				// Only forward text from assistant messages
				if !assistantMessages[part.MessageID] {
					continue
				}
				// Accumulate delta (Part.Text grows with each update)
				accumulated := part.Text
				prev := deltaLastSent[part.ID]
				newChars := len(accumulated) - prev
				log.Debug().Str("partID", part.ID).Int("len", len(accumulated)).Int("new", newChars).Msg("Text part updated")
				if newChars >= 200 {
					text := accumulated
					if len([]rune(text)) > 3000 {
						runes := []rune(text)
						text = string(runes[:3000]) + "…"
					}
					sendMsg(roomID, prefix+text)
					deltaLastSent[part.ID] = len(accumulated)
				}
				deltaAccum[part.ID] = accumulated

			case opencode.PartTypeTool:
				// Announce each tool invocation once when it appears
				if part.Tool != "" && !announcedTools[part.ID] {
					announcedTools[part.ID] = true
					sendMsg(roomID, fmt.Sprintf("%s🔧 `%s`", prefix, part.Tool))
				}
			}

		case opencode.EventListResponseTypeSessionIdle:
			props, ok := evt.Properties.(opencode.EventListResponseEventSessionIdleProperties)
			if !ok {
				continue
			}
			sid := props.SessionID
			if !tracked.contains(sid) {
				continue
			}

			// Flush any remaining buffered text for this session
			flushed := false
			for partID, accumulated := range deltaAccum {
				sent := deltaLastSent[partID]
				if len(accumulated) > sent {
					tail := accumulated[sent:]
					if len([]rune(tail)) > 3000 {
						runes := []rune(tail)
						tail = string(runes[:3000]) + "…"
					}
					sendMsg(roomID, tail)
					flushed = true
				}
			}
			deltaAccum = map[string]string{}
			deltaLastSent = map[string]int{}
			announcedTools = map[string]bool{}
			assistantMessages = map[string]bool{}

			if sid == attachedID {
				stopTyping()
				_ = flushed
			}
			// Child session idle — no extra message, root idle will follow

		case opencode.EventListResponseTypeSessionError:
			props, ok := evt.Properties.(opencode.EventListResponseEventSessionErrorProperties)
			if !ok {
				continue
			}
			if tracked.contains(props.SessionID) {
				sendMsg(roomID, "❌ Chyba v session")
			}

		case opencode.EventListResponseTypePermissionUpdated:
			perm, ok := evt.Properties.(opencode.Permission)
			if !ok {
				continue
			}
			if !tracked.contains(perm.SessionID) {
				continue
			}

			// Format and send permission message
			msg := formatPermissionMessage(&perm)
			sendMsg(roomID, msg)

			// Notify bot about pending permission
			if onPermission != nil {
				onPermission(perm.SessionID, &perm)
			}

		case opencode.EventListResponseTypeSessionCreated:
			props, ok := evt.Properties.(opencode.EventListResponseEventSessionCreatedProperties)
			if !ok {
				continue
			}
			newSession := props.Info
			// Only track if it's a child of a tracked session
			if newSession.ParentID != "" && tracked.contains(newSession.ParentID) {
				tracked.add(newSession.ID, newSession.ParentID)
				log.Info().
					Str("childID", newSession.ID).
					Str("parentID", newSession.ParentID).
					Msg("Tracking new child session")
				sendMsg(roomID, fmt.Sprintf("🔀 Child session: `%s`", newSession.ID[:8]))
			}
		}
	}

	if err := stream.Err(); err != nil {
		select {
		case <-ctx.Done():
			return nil
		default:
			return fmt.Errorf("SSE stream error: %w", err)
		}
	}
	return nil
}

// formatPermissionMessage formats a permission request as a user-friendly message.
func formatPermissionMessage(perm *opencode.Permission) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("❓ Permission Request: %s\n", perm.Title))
	if perm.Pattern != nil {
		sb.WriteString(fmt.Sprintf("Pattern: %v\n", perm.Pattern))
	}
	sb.WriteString("\nAvailable commands:\n")
	sb.WriteString("/allow-once — Grant once\n")
	sb.WriteString("/allow-always — Grant always\n")
	sb.WriteString("/deny — Reject")
	return sb.String()
}
