package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zakros-hq/zakros/minos/argus"
	"github.com/zakros-hq/zakros/minos/dispatch/fakedispatch"
	"github.com/zakros-hq/zakros/minos/storage/memstore"
	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

func newRig(t *testing.T) (*httptest.Server, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	em := audit.NewWriterEmitter("argus-test", io.Discard)
	a, err := argus.New(argus.DefaultConfig(), fakedispatch.New(), memstore.New(func() time.Time { return time.Now().UTC() }), nil, em)
	if err != nil {
		t.Fatalf("argus.New: %v", err)
	}
	srv := &server{
		audit: em,
		argus: a,
		verifier: &brokerauth.Verifier{
			Broker:    "argus",
			PublicKey: pub,
			Replay:    brokerauth.NopReplayStore{},
			Audit:     em,
		},
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts, priv
}

func mintJWT(t *testing.T, priv ed25519.PrivateKey, subject string, audience []string, scopes map[string][]string) string {
	t.Helper()
	now := time.Now().UTC()
	tok, err := jwt.Sign(priv, jwt.Claims{
		Subject:   subject,
		Issuer:    "minos",
		Audience:  audience,
		IssuedAt:  now,
		Expires:   now.Add(time.Hour),
		JTI:       "test-jti",
		McpScopes: scopes,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func postJSON(t *testing.T, url, bearer string, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestHeartbeatHappyPath(t *testing.T) {
	ts, priv := newRig(t)
	id := uuid.New()
	tok := mintJWT(t, priv, "task:"+id.String(), []string{"argus"}, map[string][]string{
		"argus": {"heartbeat"},
	})
	resp := postJSON(t, ts.URL+"/argus/heartbeat", tok, map[string]string{"task_id": id.String()})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
}

func TestHeartbeatTaskIDMismatch(t *testing.T) {
	// JWT is task-scoped to a different task — a compromised pod must
	// not heartbeat on another task's behalf.
	ts, priv := newRig(t)
	tok := mintJWT(t, priv, "task:"+uuid.NewString(), []string{"argus"}, map[string][]string{
		"argus": {"heartbeat"},
	})
	resp := postJSON(t, ts.URL+"/argus/heartbeat", tok, map[string]string{"task_id": uuid.NewString()})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestHeartbeatSubjectNotTaskScoped(t *testing.T) {
	ts, priv := newRig(t)
	id := uuid.NewString()
	tok := mintJWT(t, priv, "pod:something", []string{"argus"}, map[string][]string{
		"argus": {"heartbeat"},
	})
	resp := postJSON(t, ts.URL+"/argus/heartbeat", tok, map[string]string{"task_id": id})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestHeartbeatWrongScope(t *testing.T) {
	// Event scope must not open the heartbeat endpoint.
	ts, priv := newRig(t)
	id := uuid.NewString()
	tok := mintJWT(t, priv, "task:"+id, []string{"argus"}, map[string][]string{
		"argus": {"event"},
	})
	resp := postJSON(t, ts.URL+"/argus/heartbeat", tok, map[string]string{"task_id": id})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestHeartbeatMissingBearer(t *testing.T) {
	ts, _ := newRig(t)
	resp := postJSON(t, ts.URL+"/argus/heartbeat", "", map[string]string{"task_id": uuid.NewString()})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestEventHappyPath(t *testing.T) {
	ts, priv := newRig(t)
	tok := mintJWT(t, priv, "broker:hecate", []string{"argus"}, map[string][]string{
		"argus": {"event"},
	})
	resp := postJSON(t, ts.URL+"/argus/events", tok, map[string]any{
		"type":    "scope-deny",
		"pod":     "pod-1",
		"message": "denied",
		"fields":  map[string]string{"scope": "credentials.fetch:x"},
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
}

func TestEventMissingType(t *testing.T) {
	ts, priv := newRig(t)
	tok := mintJWT(t, priv, "broker:hecate", []string{"argus"}, map[string][]string{
		"argus": {"event"},
	})
	resp := postJSON(t, ts.URL+"/argus/events", tok, map[string]any{"message": "no type"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventAudienceMismatch(t *testing.T) {
	ts, priv := newRig(t)
	tok := mintJWT(t, priv, "broker:hecate", []string{"hecate"}, map[string][]string{
		"hecate": {"event"},
	})
	resp := postJSON(t, ts.URL+"/argus/events", tok, map[string]any{"type": "audit"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	ts, _ := newRig(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
