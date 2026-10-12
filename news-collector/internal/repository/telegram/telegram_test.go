package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeChat(t *testing.T) {
	for in, want := range map[string]string{
		"@news_channel":                "@news_channel",
		"news_channel":                 "@news_channel",
		"https://t.me/news_channel":    "@news_channel",
		"t.me/news_channel/123?single": "@news_channel",
		"-1001234567890":               "-1001234567890",
	} {
		if got, ok := NormalizeChat(in); !ok || got != want {
			t.Errorf("%q -> %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "ab", "@a b", "https://example.com/x y", "-12"} {
		if _, ok := NormalizeChat(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidToken(t *testing.T) {
	if !ValidToken("123456789:AAFxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx") || ValidToken("nope") || ValidToken("12:short") {
		t.Fatal("token validation is wrong")
	}
}

func TestSendMessageAndErrors(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") && strings.Contains(r.URL.Path, "/botGOOD/") {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
			return
		}
		if strings.Contains(r.URL.Path, "/botFLOOD/") {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
			return
		}
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked"}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	id, err := c.SendMessage(context.Background(), "GOOD", "@chan", "<b>x</b>", false)
	if err != nil || id != 42 || got["parse_mode"] != "HTML" || got["chat_id"] != "@chan" {
		t.Fatalf("%v %v %v", id, err, got)
	}
	if _, err = c.SendMessage(context.Background(), "GOOD", "-100123", "x", false); err != nil || got["chat_id"] != float64(-100123) {
		t.Fatalf("numeric chat ids must be sent as numbers: %v %v", err, got["chat_id"])
	}
	_, err = c.SendMessage(context.Background(), "FLOOD", "@c", "x", false)
	if e, ok := err.(*Error); !ok || e.RetryAfter.Seconds() != 7 || e.Permanent() {
		t.Fatalf("flood control: %v", err)
	}
	_, err = c.SendMessage(context.Background(), "KICKED", "@c", "x", false)
	if e, ok := err.(*Error); !ok || !e.Permanent() {
		t.Fatalf("403 is permanent: %v", err)
	}
}

func TestErrorsDoNotLeakTheToken(t *testing.T) {
	c := New("http://127.0.0.1:1") // nothing listens here
	_, err := c.GetMe(context.Background(), "123456:SECRETSECRETSECRETSECRETSECRETSEC")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error must not contain the token: %v", err)
	}
}
