package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	opencode "github.com/sst/opencode-sdk-go"
	"maunium.net/go/mautrix/id"
)

// PermissionRequest holds permission request data from SSE event
type PermissionRequest struct {
	ID       string
	Type     string
	Metadata map[string]interface{}
	Patterns []string
}

// QuestionRequest holds question request data from SSE event
type QuestionRequest struct {
	ID        string
	Questions []map[string]interface{}
}

// StartSSEListener starts a goroutine that reads the OpenCode SSE stream using
// the official SDK and forwards relevant events to the Matrix room.
//
// typingFn: called with true when processing starts, false when done.
// onPermission: called when a permission request arrives.
// onQuestion: called when a question request arrives.
// onTokensUpdate: called when tokens/cost info is available (inputTokens, outputTokens, cacheTokens, cost).
func StartSSEListener(
	ctx context.Context,
	cfg *Config,
	oc *OpencodeClient,
	attachedID string,
	roomID id.RoomID,
	sendMsg func(id.RoomID, string),
	lastMsgID string,
	typingFn func(bool),
	onPermission func(PermissionRequest),
	onQuestion func(QuestionRequest),
	onTokensUpdate func(int, int, int, float64),
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

			err := runSDKSSELoop(childCtx, oc, attachedID, roomID, sendMsg, lastMsgID, typingFn, onPermission, onQuestion, onTokensUpdate)
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
	onPermission func(PermissionRequest),
	onQuestion func(QuestionRequest),
	onTokensUpdate func(int, int, int, float64),
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

	// Token and cost tracking per part (to avoid duplicate counting)
	tokenTracked := map[string]bool{}
	totalTokensInput := 0
	totalTokensOutput := 0
	totalTokensCache := 0
	totalCost := 0.0

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

			case opencode.PartTypeStepFinish:
				// Track tokens and cost from step finish
				if !tokenTracked[part.ID] && part.Tokens != nil {
					tokenTracked[part.ID] = true
					// Parse tokens JSON
					if tokensMap, ok := part.Tokens.(map[string]interface{}); ok {
						if input, ok := tokensMap["input"].(float64); ok {
							totalTokensInput += int(input)
						}
						if output, ok := tokensMap["output"].(float64); ok {
							totalTokensOutput += int(output)
						}
						if cacheObj, ok := tokensMap["cache"].(map[string]interface{}); ok {
							if read, ok := cacheObj["read"].(float64); ok {
								totalTokensCache += int(read)
							}
						}
					}
				}
				// Track cost
				if part.Cost > 0 {
					totalCost += part.Cost
					onTokensUpdate(totalTokensInput, totalTokensOutput, totalTokensCache, totalCost)
				}

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

		default:
			// Handle permission.asked and question.asked events (not in SDK yet)
			eventType := string(evt.Type)
			log.Debug().Str("eventType", eventType).Msg("Unhandled event type")

			if eventType == "permission.asked" {
				// Flush any buffered text BEFORE displaying permission request
				flushPendingText(deltaAccum, deltaLastSent, sendMsg, roomID, "")
				// Parse and display permission request immediately
				var permReq PermissionRequest
				parsePermissionAskedRaw(evt, &permReq)
				log.Debug().Str("permissionID", permReq.ID).Msg("Permission request received")
				onPermission(permReq)
			} else if eventType == "question.asked" {
				// Flush any buffered text BEFORE displaying question request
				flushPendingText(deltaAccum, deltaLastSent, sendMsg, roomID, "")
				// Parse and display question request immediately
				var qReq QuestionRequest
				parseQuestionAskedRaw(evt, &qReq)
				log.Debug().Str("questionID", qReq.ID).Msg("Question request received")
				onQuestion(qReq)
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

// flushPendingText sends any buffered text to the chat before permission/question requests
// This ensures proper message ordering: buffered text appears before the request that may have triggered it
func flushPendingText(
	deltaAccum map[string]string,
	deltaLastSent map[string]int,
	sendMsg func(id.RoomID, string),
	roomID id.RoomID,
	prefix string,
) {
	for partID, accumulated := range deltaAccum {
		sent := deltaLastSent[partID]
		if len(accumulated) > sent {
			tail := accumulated[sent:]
			if len([]rune(tail)) > 3000 {
				runes := []rune(tail)
				tail = string(runes[:3000]) + "…"
			}
			sendMsg(roomID, prefix+tail)
			// Update deltaLastSent so we don't resend this text
			deltaLastSent[partID] = len(accumulated)
		}
	}
}

// parsePermissionAskedRaw parses permission.asked event and stores result in req
// Used to defer displaying the request until after text is flushed
func parsePermissionAskedRaw(evt opencode.EventListResponse, req *PermissionRequest) {
	if req == nil {
		return
	}

	// Parse raw JSON event to extract properties
	rawJSON := evt.JSON.RawJSON()

	var rawEvent map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawEvent); err != nil {
		log.Warn().Err(err).Msg("Failed to unmarshal permission.asked event JSON")
		return
	}

	// Extract properties object
	propsInterface, ok := rawEvent["properties"]
	if !ok {
		log.Warn().Msg("permission.asked event missing properties field")
		return
	}

	props, ok := propsInterface.(map[string]interface{})
	if !ok {
		log.Warn().Interface("properties", propsInterface).Msg("permission.asked properties is not an object")
		return
	}

	parsePermissionAsked(props, req)
}

// parseQuestionAskedRaw parses question.asked event and stores result in req
// Used to defer displaying the request until after text is flushed
func parseQuestionAskedRaw(evt opencode.EventListResponse, req *QuestionRequest) {
	if req == nil {
		return
	}

	// Parse raw JSON event to extract properties
	rawJSON := evt.JSON.RawJSON()

	var rawEvent map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawEvent); err != nil {
		log.Warn().Err(err).Msg("Failed to unmarshal question.asked event JSON")
		return
	}

	// Extract properties object
	propsInterface, ok := rawEvent["properties"]
	if !ok {
		log.Warn().Msg("question.asked event missing properties field")
		return
	}

	props, ok := propsInterface.(map[string]interface{})
	if !ok {
		log.Warn().Interface("properties", propsInterface).Msg("question.asked properties is not an object")
		return
	}

	parseQuestionAsked(props, req)
}

// handlePermissionAskedRaw parses permission.asked event from raw EventListResponse
func handlePermissionAskedRaw(evt opencode.EventListResponse, callback func(PermissionRequest)) {
	if callback == nil {
		return
	}

	// Parse raw JSON event to extract properties
	// SDK doesn't fully parse unknown event types, so we need to do it manually
	rawJSON := evt.JSON.RawJSON()

	var rawEvent map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawEvent); err != nil {
		log.Warn().Err(err).Msg("Failed to unmarshal permission.asked event JSON")
		return
	}

	// Extract properties object
	propsInterface, ok := rawEvent["properties"]
	if !ok {
		log.Warn().Msg("permission.asked event missing properties field")
		return
	}

	props, ok := propsInterface.(map[string]interface{})
	if !ok {
		log.Warn().Interface("properties", propsInterface).Msg("permission.asked properties is not an object")
		return
	}

	handlePermissionAsked(props, callback)
}

// handleQuestionAskedRaw parses question.asked event from raw EventListResponse
func handleQuestionAskedRaw(evt opencode.EventListResponse, callback func(QuestionRequest)) {
	if callback == nil {
		return
	}

	// Parse raw JSON event to extract properties
	rawJSON := evt.JSON.RawJSON()

	var rawEvent map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawEvent); err != nil {
		log.Warn().Err(err).Msg("Failed to unmarshal question.asked event JSON")
		return
	}

	// Extract properties object
	propsInterface, ok := rawEvent["properties"]
	if !ok {
		log.Warn().Msg("question.asked event missing properties field")
		return
	}

	props, ok := propsInterface.(map[string]interface{})
	if !ok {
		log.Warn().Interface("properties", propsInterface).Msg("question.asked properties is not an object")
		return
	}

	handleQuestionAsked(props, callback)
}

// parsePermissionAsked parses permission properties into a PermissionRequest (no callback)
func parsePermissionAsked(props interface{}, req *PermissionRequest) {
	if req == nil {
		return
	}

	// Try to parse as a map (generic JSON object)
	propMap, ok := props.(map[string]interface{})
	if !ok {
		log.Warn().Interface("properties", props).Msg("Failed to parse permission.asked properties")
		return
	}

	// Extract ID
	if id, ok := propMap["id"].(string); ok {
		req.ID = id
	}

	// Extract permission type
	if permType, ok := propMap["permission"].(string); ok {
		req.Type = permType
	}

	// Extract metadata
	if meta, ok := propMap["metadata"].(map[string]interface{}); ok {
		req.Metadata = meta
	}

	// Extract patterns
	if patterns, ok := propMap["patterns"].([]interface{}); ok {
		req.Patterns = make([]string, 0, len(patterns))
		for _, p := range patterns {
			if str, ok := p.(string); ok {
				req.Patterns = append(req.Patterns, str)
			}
		}
	}

	log.Info().Str("permissionID", req.ID).Str("type", req.Type).Msg("Permission request parsed")
}

// handlePermissionAsked parses permission.asked event and calls the callback
func handlePermissionAsked(props interface{}, callback func(PermissionRequest)) {
	if callback == nil {
		return
	}

	req := PermissionRequest{}
	parsePermissionAsked(props, &req)
	callback(req)
}

// parseQuestionAsked parses question properties into a QuestionRequest (no callback)
func parseQuestionAsked(props interface{}, req *QuestionRequest) {
	if req == nil {
		return
	}

	// Try to parse as a map
	propMap, ok := props.(map[string]interface{})
	if !ok {
		log.Warn().Interface("properties", props).Msg("Failed to parse question.asked properties")
		return
	}

	// Extract ID
	if id, ok := propMap["id"].(string); ok {
		req.ID = id
	}

	// Extract questions
	if questions, ok := propMap["questions"].([]interface{}); ok {
		req.Questions = make([]map[string]interface{}, 0, len(questions))
		for _, q := range questions {
			if qMap, ok := q.(map[string]interface{}); ok {
				req.Questions = append(req.Questions, qMap)
			}
		}
	}

	log.Info().Str("questionID", req.ID).Int("questionCount", len(req.Questions)).Msg("Question request parsed")
}

// handleQuestionAsked parses question.asked event and calls the callback
func handleQuestionAsked(props interface{}, callback func(QuestionRequest)) {
	if callback == nil {
		return
	}

	req := QuestionRequest{}
	parseQuestionAsked(props, &req)
	callback(req)
}
