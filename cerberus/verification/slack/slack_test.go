package slack_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/zakros-hq/zakros/cerberus/verification/slack"
)

// signRequest builds a Slack-shaped POST with a valid signature.
func signRequest(t *testing.T, secret []byte, ts time.Time, body string) *http.Request {
	t.Helper()
	tsStr := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("v0:"))
	mac.Write([]byte(tsStr))
	mac.Write([]byte(":"))
	mac.Write([]byte(body))
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))
	r := httptest.NewRequest("POST", "/webhooks/slack", bytes.NewReader([]byte(body)))
	r.Header.Set("X-Slack-Signature", sig)
	r.Header.Set("X-Slack-Request-Timestamp", tsStr)
	return r
}

func TestVerifyHappyPath(t *testing.T) {
	now := time.Now().UTC()
	secret := []byte("test-signing-secret")
	body := `{"type":"event_callback","event":{"type":"app_mention","text":"hi"}}`

	v := slack.NewVerifier(secret, nil).WithClock(func() time.Time { return now })
	r := signRequest(t, secret, now, body)

	ev, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.Source != "slack" {
		t.Errorf("source: %q", ev.Source)
	}
	if ev.Type != "event_callback" {
		t.Errorf("type: %q", ev.Type)
	}
	if string(ev.Body) != body {
		t.Errorf("body mismatch")
	}
}

func TestVerifyBadSignature(t *testing.T) {
	now := time.Now().UTC()
	v := slack.NewVerifier([]byte("right-secret"), nil).WithClock(func() time.Time { return now })
	// Sign with the wrong secret.
	r := signRequest(t, []byte("wrong-secret"), now, `{"type":"x"}`)
	if _, err := v.Verify(context.Background(), r); !errors.Is(err, slack.ErrInvalidSignature) {
		t.Errorf("want ErrInvalidSignature, got %v", err)
	}
}

func TestVerifyTimestampSkew(t *testing.T) {
	now := time.Now().UTC()
	secret := []byte("s")
	v := slack.NewVerifier(secret, nil).WithClock(func() time.Time { return now })
	// Body signed 10 minutes ago, beyond the 5-minute default window.
	old := now.Add(-10 * time.Minute)
	r := signRequest(t, secret, old, `{"type":"x"}`)
	if _, err := v.Verify(context.Background(), r); !errors.Is(err, slack.ErrTimestampSkew) {
		t.Errorf("want ErrTimestampSkew, got %v", err)
	}
}

func TestVerifyMissingHeaders(t *testing.T) {
	now := time.Now().UTC()
	v := slack.NewVerifier([]byte("s"), nil).WithClock(func() time.Time { return now })

	t.Run("missing signature", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte(`{}`)))
		r.Header.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", now.Unix()))
		if _, err := v.Verify(context.Background(), r); !errors.Is(err, slack.ErrMissingHeader) {
			t.Errorf("want ErrMissingHeader, got %v", err)
		}
	})

	t.Run("missing timestamp", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte(`{}`)))
		r.Header.Set("X-Slack-Signature", "v0=00")
		if _, err := v.Verify(context.Background(), r); !errors.Is(err, slack.ErrMissingHeader) {
			t.Errorf("want ErrMissingHeader, got %v", err)
		}
	})
}

func TestVerifyReplay(t *testing.T) {
	now := time.Now().UTC()
	secret := []byte("s")
	store := &memReplay{seen: map[string]bool{}}
	v := slack.NewVerifier(secret, store).WithClock(func() time.Time { return now })
	body := `{"type":"x"}`

	r1 := signRequest(t, secret, now, body)
	if _, err := v.Verify(context.Background(), r1); err != nil {
		t.Fatalf("first call: %v", err)
	}
	r2 := signRequest(t, secret, now, body)
	if _, err := v.Verify(context.Background(), r2); !errors.Is(err, slack.ErrReplay) {
		t.Errorf("want ErrReplay, got %v", err)
	}
}

// memReplay is a tiny in-memory ReplayStore for the test.
type memReplay struct{ seen map[string]bool }

func (m *memReplay) Seen(_ context.Context, id string, _ time.Time) (bool, error) {
	if m.seen[id] {
		return true, nil
	}
	m.seen[id] = true
	return false, nil
}
