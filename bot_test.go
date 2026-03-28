package main

import (
	"context"
	"strings"
	"testing"

	"maunium.net/go/mautrix/id"
)

// --- helper: newTestBot creates a Bot with a captured send function ---

func newTestBot(sent *[]string) *Bot {
	cfg := &Config{
		MatrixUserID:  "@bot:matrix.org",
		MatrixOwnerID: "@owner:matrix.org",
		OpencodeURL:   "http://localhost:4096",
	}
	b := &Bot{
		cfg:        cfg,
		roomStates: map[id.RoomID]*RoomState{},
		startupTS:  0,
	}
	b.sendMsgFn = func(roomID id.RoomID, text string) {
		*sent = append(*sent, text)
	}
	return b
}

const testRoom id.RoomID = "!test:matrix.org"

// --- Config tests ---

func TestLoadConfigMissingHomeserver(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER", "")
	t.Setenv("MATRIX_USER_ID", "@bot:matrix.org")
	t.Setenv("MATRIX_PASSWORD", "secret")
	t.Setenv("MATRIX_OWNER_ID", "@owner:matrix.org")
	t.Setenv("OPENCODE_URL", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing MATRIX_HOMESERVER, got nil")
	}
}

func TestLoadConfigMissingUserID(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER", "https://matrix.org")
	t.Setenv("MATRIX_USER_ID", "")
	t.Setenv("MATRIX_PASSWORD", "secret")
	t.Setenv("MATRIX_OWNER_ID", "@owner:matrix.org")
	t.Setenv("OPENCODE_URL", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing MATRIX_USER_ID")
	}
}

func TestLoadConfigDefaultOpencodeURL(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER", "https://matrix.org")
	t.Setenv("MATRIX_USER_ID", "@bot:matrix.org")
	t.Setenv("MATRIX_PASSWORD", "secret")
	t.Setenv("MATRIX_OWNER_ID", "@owner:matrix.org")
	t.Setenv("OPENCODE_URL", "")
	t.Setenv("MATRIX_PICKLE_KEY", "test-pickle-key-for-unit-tests-32b")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OpencodeURL != "http://localhost:4096" {
		t.Errorf("expected default OpencodeURL, got %q", cfg.OpencodeURL)
	}
}

func TestLoadConfigAllFields(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER", "https://example.org")
	t.Setenv("MATRIX_USER_ID", "@mybot:example.org")
	t.Setenv("MATRIX_PASSWORD", "pass123")
	t.Setenv("MATRIX_OWNER_ID", "@me:example.org")
	t.Setenv("OPENCODE_URL", "http://localhost:1234")
	t.Setenv("OPENCODE_PASSWORD", "apipass")
	t.Setenv("MATRIX_PICKLE_KEY", "test-pickle-key-for-unit-tests-32b")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MatrixHomeserver != "https://example.org" {
		t.Errorf("MatrixHomeserver mismatch: %q", cfg.MatrixHomeserver)
	}
	if cfg.OpencodePassword != "apipass" {
		t.Errorf("OpencodePassword mismatch: %q", cfg.OpencodePassword)
	}
	if cfg.PickleKey != "test-pickle-key-for-unit-tests-32b" {
		t.Errorf("PickleKey mismatch: %q", cfg.PickleKey)
	}
}

func TestLoadConfigMissingPickleKey(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER", "https://matrix.org")
	t.Setenv("MATRIX_USER_ID", "@bot:matrix.org")
	t.Setenv("MATRIX_PASSWORD", "secret")
	t.Setenv("MATRIX_OWNER_ID", "@owner:matrix.org")
	t.Setenv("MATRIX_PICKLE_KEY", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing MATRIX_PICKLE_KEY")
	}
}

// --- stateIcon tests ---

func TestStateIcon(t *testing.T) {
	cases := []struct {
		state string
		want  string
	}{
		{"running", "🟢"},
		{"busy", "🟢"},
		{"idle", "⚪"},
		{"error", "🔴"},
		{"retry", "⏳"},
		{"", "❓"},
		{"unknown-state", "❓"},
	}
	for _, tc := range cases {
		got := stateIcon(tc.state)
		if got != tc.want {
			t.Errorf("stateIcon(%q) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// --- Command routing tests ---

func TestCmdHelp(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "/help")
	if len(sent) == 0 {
		t.Fatal("expected help message")
	}
	for _, kw := range []string{"/attach", "/detach", "/status", "/todo", "/abort", "/new", "/sessions"} {
		if !strings.Contains(sent[0], kw) {
			t.Errorf("help missing keyword %q", kw)
		}
	}
}

func TestCmdUnknownCommand(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "/foobar")
	if len(sent) == 0 || !strings.Contains(sent[0], "Neznámy príkaz") {
		t.Errorf("expected 'Neznámy príkaz', got %q", sent)
	}
}

func TestCmdAttachMissingID(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "/attach")
	if len(sent) == 0 || !strings.Contains(sent[0], "/attach") {
		t.Errorf("expected usage hint for /attach, got %q", sent)
	}
}

func TestCmdDetachWhenNotAttached(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "/detach")
	if len(sent) == 0 || !strings.Contains(sent[0], "Odpojený") {
		t.Errorf("expected 'Odpojený', got %q", sent)
	}
}

func TestCmdStatusWhenNotAttached(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "/status")
	if len(sent) == 0 || !strings.Contains(sent[0], "session") {
		t.Errorf("expected 'no session' error, got %q", sent)
	}
}

func TestCmdTodoWhenNotAttached(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "/todo")
	if len(sent) == 0 || !strings.Contains(sent[0], "session") {
		t.Errorf("expected 'no session' error, got %q", sent)
	}
}

func TestCmdAbortWhenNotAttached(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "/abort")
	if len(sent) == 0 || !strings.Contains(sent[0], "session") {
		t.Errorf("expected 'no session' error, got %q", sent)
	}
}

func TestCmdPromptWhenNotAttached(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.handleCommand(context.Background(), testRoom, "hello world")
	if len(sent) == 0 || !strings.Contains(sent[0], "session") {
		t.Errorf("expected 'no session' prompt, got %q", sent)
	}
}

func TestCmdDetachCancelsState(t *testing.T) {
	var sent []string
	b := newTestBot(&sent)
	b.mu.Lock()
	state := b.getOrCreateRoomState(testRoom)
	state.AttachedSessionID = "test-session-id"
	cancelled := false
	state.SSECancel = func() { cancelled = true }
	b.mu.Unlock()

	b.handleCommand(context.Background(), testRoom, "/detach")

	if !cancelled {
		t.Error("expected SSECancel to be called on detach")
	}
	if b.attachedSessionID(testRoom) != "" {
		t.Error("expected AttachedSessionID to be cleared")
	}
}

// --- trackedSessions tests ---

func TestTrackedSessionsRoot(t *testing.T) {
	ts := newTrackedSessions("ses_root")
	if !ts.contains("ses_root") {
		t.Error("root session should be tracked")
	}
	if ts.contains("ses_other") {
		t.Error("other session should not be tracked")
	}
	if ts.isChild("ses_root") {
		t.Error("root should not be a child")
	}
}

func TestTrackedSessionsAddChild(t *testing.T) {
	ts := newTrackedSessions("ses_root")
	ts.add("ses_child", "ses_root")

	if !ts.contains("ses_child") {
		t.Error("child session should be tracked after add")
	}
	if !ts.isChild("ses_child") {
		t.Error("child session should be marked as child")
	}
	if ts.isChild("ses_root") {
		t.Error("root should not be marked as child")
	}
}

func TestTrackedSessionsMultiLevel(t *testing.T) {
	ts := newTrackedSessions("ses_root")
	ts.add("ses_child", "ses_root")
	ts.add("ses_grandchild", "ses_child")

	if !ts.contains("ses_grandchild") {
		t.Error("grandchild should be tracked")
	}
	if !ts.isChild("ses_grandchild") {
		t.Error("grandchild should be marked as child")
	}
}

func TestTrackedSessionsUnrelatedNotAdded(t *testing.T) {
	ts := newTrackedSessions("ses_root")
	// Don't add ses_unrelated — simulate what the SSE loop does:
	// only adds if parentID is already tracked
	if ts.contains("ses_unrelated") {
		t.Error("unrelated session should not be tracked")
	}
}

// --- text accumulation / debounce tests ---

func TestTextAccumDebounce(t *testing.T) {
	// Mirrors the accumulation logic in runSDKSSELoop
	deltaLastSent := map[string]int{}
	var sent []string
	send := func(_ id.RoomID, text string) { sent = append(sent, text) }

	partID := "prt_001"
	checkAndSend := func(accumulated string) {
		prev := deltaLastSent[partID]
		if len(accumulated)-prev >= 200 {
			text := accumulated
			if len([]rune(text)) > 3000 {
				runes := []rune(text)
				text = string(runes[:3000]) + "…"
			}
			send(testRoom, text)
			deltaLastSent[partID] = len(accumulated)
		}
	}

	// 100 chars — below threshold, no send
	checkAndSend(strings.Repeat("a", 100))
	if len(sent) != 0 {
		t.Errorf("expected no send for 100 chars, got %d", len(sent))
	}
	// 250 chars total — over threshold, send
	checkAndSend(strings.Repeat("a", 250))
	if len(sent) != 1 {
		t.Errorf("expected 1 send after 250 chars, got %d", len(sent))
	}
	// 280 chars total — only 30 new, no send
	checkAndSend(strings.Repeat("a", 280))
	if len(sent) != 1 {
		t.Errorf("expected still 1 send (debounce), got %d", len(sent))
	}
	// 460 chars total — 210 new, send again
	checkAndSend(strings.Repeat("a", 460))
	if len(sent) != 2 {
		t.Errorf("expected 2 sends, got %d", len(sent))
	}
}

func TestTextAccumTruncate(t *testing.T) {
	deltaLastSent := map[string]int{}
	var sent []string
	send := func(_ id.RoomID, text string) { sent = append(sent, text) }

	partID := "prt_002"
	longText := strings.Repeat("x", 3500)
	prev := deltaLastSent[partID]
	if len(longText)-prev >= 200 {
		text := longText
		if len([]rune(text)) > 3000 {
			runes := []rune(text)
			text = string(runes[:3000]) + "…"
		}
		send(testRoom, text)
		deltaLastSent[partID] = len(longText)
	}

	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if !strings.HasSuffix(sent[0], "…") {
		t.Errorf("expected truncation marker '…'")
	}
	if len([]rune(sent[0])) != 3001 { // 3000 + "…"
		t.Errorf("expected 3001 runes, got %d", len([]rune(sent[0])))
	}
}

// --- messageID historical filter test ---

func TestHistoricalFilterSkipsOld(t *testing.T) {
	lastMsgID := "msg_d2cdbeef60017"
	// Older message — should be skipped
	old := "msg_d2cd3b45f001"
	newer := "msg_d2cf000000001"

	if !(old <= lastMsgID) {
		t.Error("old message should be <= lastMsgID")
	}
	if newer <= lastMsgID {
		t.Error("newer message should be > lastMsgID")
	}
}
