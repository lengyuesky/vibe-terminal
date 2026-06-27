package protocol

import (
	"encoding/json"
	"testing"
)

func TestDecodeEnvelopeRejectsMissingType(t *testing.T) {
	_, err := DecodeEnvelope([]byte(`{"payload":{}}`))
	if err == nil {
		t.Fatal("expected missing type error")
	}
}

func TestEncodeAndDecodeStartSession(t *testing.T) {
	payload := StartSession{
		SessionID:        "sess-1",
		ShellPath:        "/bin/bash",
		WorkingDirectory: "/home/dev",
		Cols:             120,
		Rows:             32,
	}
	data, err := EncodeEnvelope(TypeStartSession, payload)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	env, err := DecodeEnvelope(data)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Type != TypeStartSession {
		t.Fatalf("type = %q, want %q", env.Type, TypeStartSession)
	}
	var got StartSession
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.SessionID != "sess-1" || got.Cols != 120 || got.Rows != 32 {
		t.Fatalf("unexpected payload: %#v", got)
	}
}
