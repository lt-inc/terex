package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"math"
	"math/bits"
	"net/url"
	"strings"
	"unicode"
)

// ---- vectors ----

func dot(a, b []float32) float64 {
	n := min(len(a), len(b))
	var s float64
	for i := 0; i < n; i++ {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func norm(a []float32) float64 { return math.Sqrt(dot(a, a)) }

// cosine returns 0 for empty or mismatched vectors, so a bad embedding never matches anything.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	d := norm(a) * norm(b)
	if d == 0 {
		return 0
	}
	return dot(a, b) / d
}

// meanUpdate folds one more vector into a centroid built from n vectors.
func meanUpdate(centroid []float32, n int, v []float32) []float32 {
	if len(centroid) != len(v) || n < 1 {
		return append([]float32(nil), v...)
	}
	out := make([]float32, len(v))
	for i := range v {
		out[i] = (centroid[i]*float32(n) + v[i]) / float32(n+1)
	}
	return out
}

// ---- simhash ----

var tokenSplit = func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// simhash is a 64-bit locality-sensitive fingerprint over word 3-shingles: near-identical
// texts differ in a handful of bits.
func simhash(text string) uint64 {
	words := strings.FieldsFunc(strings.ToLower(text), tokenSplit)
	if len(words) < 3 {
		return 0
	}
	var weights [64]int
	for i := 0; i+3 <= len(words); i++ {
		h := fnv.New64a()
		h.Write([]byte(words[i] + " " + words[i+1] + " " + words[i+2]))
		sum := h.Sum64()
		for b := 0; b < 64; b++ {
			if sum&(1<<uint(b)) != 0 {
				weights[b]++
			} else {
				weights[b]--
			}
		}
	}
	var out uint64
	for b := 0; b < 64; b++ {
		if weights[b] > 0 {
			out |= 1 << uint(b)
		}
	}
	return out
}

func hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// ---- text ----

// detectLang guesses ru/en from the share of Cyrillic letters.
func detectLang(s string) string {
	var cyr, letters int
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.Is(unicode.Cyrillic, r) {
				cyr++
			}
		}
	}
	if letters > 0 && float64(cyr)/float64(letters) > 0.3 {
		return "ru"
	}
	return "en"
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// trimToLimit shortens text to at most n characters, preferring a paragraph or sentence end.
func trimToLimit(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	cut := string(r[:n])
	for _, sep := range []string{"\n\n", ". ", "! ", "? "} {
		if i := strings.LastIndex(cut, sep); i > n/2 {
			return strings.TrimSpace(cut[:i+len(strings.TrimRight(sep, " \n"))])
		}
	}
	return strings.TrimSpace(cut) + "…"
}

var trackingParams = map[string]bool{"fbclid": true, "gclid": true, "yclid": true, "mc_cid": true, "mc_eid": true, "ref": true, "from": true}

// normalizeURL makes the same article compare equal across feeds and tracking parameters.
func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") || trackingParams[strings.ToLower(k)] {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

func urlHash(raw string) string {
	sum := sha256.Sum256([]byte(normalizeURL(raw)))
	return hex.EncodeToString(sum[:])
}

// decodeGNewsLink extracts the publisher URL from an old-style Google News article id
// (base64 of a protobuf holding the URL). Newer opaque ids return "".
func decodeGNewsLink(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Host != "news.google.com" {
		return ""
	}
	id := u.Path[strings.LastIndex(u.Path, "/")+1:]
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(id, "="))
	if err != nil {
		return ""
	}
	i := strings.Index(string(raw), "http")
	if i < 0 {
		return ""
	}
	end := i
	for end < len(raw) && raw[end] >= 0x21 && raw[end] <= 0x7e {
		end++
	}
	out := string(raw[i:end])
	if p, err := url.Parse(out); err != nil || p.Host == "" {
		return ""
	}
	return out
}

// ---- secrets ----

var ErrNoSecretKey = errors.New("NEWS_SECRET_KEY is not configured")

// Sealer encrypts bot tokens at rest with AES-256-GCM.
type Sealer struct{ aead cipher.AEAD }

// NewSealer takes a base64-encoded 32-byte key. An empty key returns a nil Sealer.
func NewSealer(b64 string) (*Sealer, error) {
	if strings.TrimSpace(b64) == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(key) != 32 {
		return nil, errors.New("NEWS_SECRET_KEY must be 32 bytes, base64-encoded")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plain string) ([]byte, error) {
	if s == nil {
		return nil, ErrNoSecretKey
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (s *Sealer) Open(data []byte) (string, error) {
	if s == nil {
		return "", ErrNoSecretKey
	}
	n := s.aead.NonceSize()
	if len(data) < n {
		return "", errors.New("stored secret is corrupted")
	}
	plain, err := s.aead.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return "", errors.New("stored secret cannot be decrypted (wrong NEWS_SECRET_KEY?)")
	}
	return string(plain), nil
}
