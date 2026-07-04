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

func TestEncodeEnvelopeWithRequestRoundTrip(t *testing.T) {
	payload := FsRead{Path: "/tmp/demo.txt", Offset: 262144, Length: 262144}
	data, err := EncodeEnvelopeWithRequest(TypeFsRead, "req-1", payload)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	env, err := DecodeEnvelope(data)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Type != TypeFsRead || env.RequestID != "req-1" {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	var got FsRead
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Path != "/tmp/demo.txt" || got.Offset != 262144 || got.Length != 262144 {
		t.Fatalf("unexpected payload: %#v", got)
	}
}

func TestAgentHelloCarriesCapabilities(t *testing.T) {
	raw := []byte(`{"device_id":"dev-1","credential":"c","platform":"linux","agent_version":"0.1.0","protocol_version":"v1","capabilities":["fs"],"sessions":[]}`)
	var hello AgentHello
	if err := json.Unmarshal(raw, &hello); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if len(hello.Capabilities) != 1 || hello.Capabilities[0] != CapabilityFs {
		t.Fatalf("capabilities = %#v", hello.Capabilities)
	}
}

func TestFsErrorRoundTrip(t *testing.T) {
	data, err := EncodeEnvelopeWithRequest(TypeFsError, "req-2", FsError{Code: "not_found", Message: "missing"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	env, err := DecodeEnvelope(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got FsError
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got.Code != "not_found" {
		t.Fatalf("code = %q", got.Code)
	}
}
