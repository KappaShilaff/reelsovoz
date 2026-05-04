package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestLoggerWritesLogatronCompatibleJSON(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out)

	logger.Info("test message", "username", "ReelsovozBot")

	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("log line is not json: %v; line=%q", err, out.String())
	}

	if payload["level"] != slog.LevelInfo.String() {
		t.Fatalf("level = %v, want %q", payload["level"], slog.LevelInfo.String())
	}
	if payload["message"] != "test message" {
		t.Fatalf("message = %v", payload["message"])
	}
	if _, ok := payload["msg"]; ok {
		t.Fatalf("unexpected msg key in payload: %v", payload)
	}
	if payload["username"] != "ReelsovozBot" {
		t.Fatalf("username = %v", payload["username"])
	}
	if _, ok := payload["time"]; !ok {
		t.Fatalf("missing time key in payload: %v", payload)
	}
}
