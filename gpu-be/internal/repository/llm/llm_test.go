package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
)

func TestBuildPromptHasLimitsHintsAndIDs(t *testing.T) {
	p := BuildPrompt([]domain.Sentence{
		{ID: 0, Start: 0, End: 4.5, Text: "Hello there."},
		{ID: 1, Start: 5, End: 9, Text: "Funny line!", Tags: []string{"energy:high", "laugh"}},
	}, domain.ClipLimits{MinSec: 15, MaxSec: 180, MaxClips: 12})
	for _, want := range []string{"at least 15 and at most 180", "at most 12 clips", "[0] 0.0s +4.5s Hello there.", "[1] 5.0s +4.0s {energy:high laugh} Funny line!", "never cut a thought in half"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt misses %q:\n%s", want, p)
		}
	}
}

func TestParsePicks(t *testing.T) {
	got, err := ParsePicks("```json\n{\"highlights\":[{\"from\":3,\"to\":9,\"title\":\" Joke \",\"hashtags\":[\"#fun\",\" life \",\"\"]}]}\n```")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if got[0].From != 3 || got[0].To != 9 || got[0].Title != "Joke" || len(got[0].Hashtags) != 2 || got[0].Hashtags[0] != "fun" {
		t.Fatalf("bad pick %+v", got[0])
	}
	got, err = ParsePicks(`{"highlights":[{"from":1,"to":2,"title":"x","hashtags":[]}]}`)
	if err != nil || len(got) != 1 || len(got[0].Hashtags) != 2 || got[0].Hashtags[0] != "#hype" || got[0].Hashtags[1] != "#viral" {
		t.Fatalf("fallback hashtags failed: %+v err=%v", got, err)
	}
	if _, err := ParsePicks(`{"highlights":[{"from":1,"to":2,"title":"x"}]}`); err == nil || !strings.Contains(err.Error(), "missing hashtags") {
		t.Fatalf("missing required fields must error, got: %v", err)
	}
	if _, err := ParsePicks("not json"); err == nil {
		t.Fatal("expected error")
	}
}

// okBody is a valid OpenRouter answer with one pick (sentences 1..4).
var okBody = func() string {
	inner := `{"highlights":[{"from":1,"to":4,"title":"T","hashtags":["a"]}]}`
	b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": inner}}}})
	return string(b)
}()

func newTestClient(h http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	c := New("key", "m")
	c.URL, c.Backoff = srv.URL, time.Millisecond
	return c, srv
}

var oneSentence = []domain.Sentence{{ID: 0, Start: 0, End: 5, Text: "hello"}}
var testLim = domain.ClipLimits{MinSec: 15, MaxSec: 180, MaxClips: 5}

func TestWhitespaceOnlyBodyIsRetried(t *testing.T) {
	var calls int32
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.Write([]byte(strings.Repeat("\n         \n", 60))) // OpenRouter keep-alive blanks, no result
			return
		}
		w.Write([]byte("\n\n" + okBody))
	})
	defer srv.Close()
	got, err := c.Find(context.Background(), oneSentence, testLim)
	if err != nil || len(got) != 1 || got[0].From != 1 || atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("got=%+v err=%v calls=%d", got, err, calls)
	}
}

func TestEmptyBodyErrorIsReadable(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("\n   \n", 200))) })
	defer srv.Close()
	_, err := c.Find(context.Background(), oneSentence, testLim)
	if err == nil || !strings.Contains(err.Error(), "empty body") || !strings.Contains(err.Error(), "3 attempts") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(err.Error(), "\n") > 0 || len(err.Error()) > 600 {
		t.Fatalf("error must not carry the whitespace flood: %q", err.Error())
	}
}

func TestErrorObjectTransientVsFatal(t *testing.T) {
	var calls int32
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Write([]byte(`{"error":{"code":429,"message":"rate limited"}}`))
			return
		}
		w.Write([]byte(okBody))
	})
	if _, err := c.Find(context.Background(), oneSentence, testLim); err != nil || calls != 2 {
		t.Fatalf("429 must be retried: err=%v calls=%d", err, calls)
	}
	srv.Close()

	calls = 0
	c, srv = newTestClient(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`{"error":{"code":400,"message":"model does not support json_object"}}`))
	})
	defer srv.Close()
	_, err := c.Find(context.Background(), oneSentence, testLim)
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "does not support json_object") {
		t.Fatalf("400 must fail fast with the provider message: err=%v calls=%d", err, calls)
	}
}

func TestHTTP5xxRetriedHTTP401Not(t *testing.T) {
	var calls int32
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	})
	defer srv.Close()
	if _, err := c.Find(context.Background(), oneSentence, testLim); err == nil || calls != 1 {
		t.Fatalf("401 must not be retried: err=%v calls=%d", err, calls)
	}
	calls = 0
	c2, srv2 := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(okBody))
	})
	defer srv2.Close()
	if _, err := c2.Find(context.Background(), oneSentence, testLim); err != nil || calls != 2 {
		t.Fatalf("502 must be retried: err=%v calls=%d", err, calls)
	}
}

func TestLongTranscriptIsChunked(t *testing.T) {
	var sents []domain.Sentence
	for i := 0; i < 3000; i++ { // ~3000 * (60+40) chars = 300k chars -> several chunks
		sents = append(sents, domain.Sentence{ID: i, Start: float64(i), End: float64(i) + 0.9, Text: strings.Repeat("x", 60)})
	}
	chunks := splitSentences(sents, maxChunkChars)
	if len(chunks) < 3 {
		t.Fatalf("want >=3 chunks, got %d", len(chunks))
	}
	total := 0
	for i, ch := range chunks {
		total += len(ch)
		if i > 0 && ch[0].ID != chunks[i-1][len(chunks[i-1])-1].ID+1 {
			t.Fatal("chunks must be consecutive and gap-free")
		}
	}
	if total != len(sents) {
		t.Fatalf("lost sentences: %d of %d", total, len(sents))
	}
	var calls int32
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1); w.Write([]byte(okBody)) })
	defer srv.Close()
	got, err := c.Find(context.Background(), sents, testLim)
	if err != nil || int(calls) != len(chunks) || len(got) != len(chunks) {
		t.Fatalf("one request per chunk expected: err=%v calls=%d chunks=%d picks=%d", err, calls, len(chunks), len(got))
	}
}

func TestMergeRoundRobin(t *testing.T) {
	a := []domain.Pick{{From: 1}, {From: 2}}
	b := []domain.Pick{{From: 10}}
	got := mergeRoundRobin([][]domain.Pick{a, b})
	if len(got) != 3 || got[0].From != 1 || got[1].From != 10 || got[2].From != 2 {
		t.Fatalf("bad order: %+v", got)
	}
}

func TestReasoningEffortIsSentWhenConfigured(t *testing.T) {
	var got map[string]any
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(okBody))
	})
	defer srv.Close()
	if _, err := c.Find(context.Background(), oneSentence, testLim); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["reasoning"]; ok {
		t.Fatalf("reasoning must not be sent by default: %v", got)
	}
	c.Effort = "low"
	if _, err := c.Find(context.Background(), oneSentence, testLim); err != nil {
		t.Fatal(err)
	}
	r, _ := got["reasoning"].(map[string]any)
	if r["effort"] != "low" || got["response_format"] == nil {
		t.Fatalf("effort not sent: %v", got)
	}
}

func TestChunkCharsIsConfigurable(t *testing.T) {
	var sents []domain.Sentence
	for i := 0; i < 200; i++ {
		sents = append(sents, domain.Sentence{ID: i, Start: float64(i), End: float64(i) + 0.9, Text: strings.Repeat("x", 60)})
	}
	var calls int32
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1); w.Write([]byte(okBody)) })
	defer srv.Close()
	c.ChunkChars = 1_000_000 // a 1M-token model: one request is enough
	if _, err := c.Find(context.Background(), sents, testLim); err != nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	atomic.StoreInt32(&calls, 0)
	c.ChunkChars = 5000
	if _, err := c.Find(context.Background(), sents, testLim); err != nil || calls < 3 {
		t.Fatalf("small chunks expected, err=%v calls=%d", err, calls)
	}
}

func TestMissingRequiredFieldsIsRetried(t *testing.T) {
	var calls int32
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			bad := `{"highlights":[{"from":1,"to":4,"title":"T"}]}`
			resp, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": bad}}}})
			w.Write(resp)
			return
		}
		w.Write([]byte(okBody))
	})
	defer srv.Close()
	got, err := c.Find(context.Background(), oneSentence, testLim)
	if err != nil || len(got) != 1 || calls != 2 {
		t.Fatalf("missing fields should trigger retry: got=%+v err=%v calls=%d", got, err, calls)
	}
}
