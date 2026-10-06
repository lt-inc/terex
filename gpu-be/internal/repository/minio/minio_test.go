package minio

import (
	"context"
	"net/url"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
)

func TestPublicHTTPSPresigningWithInternalHTTP(t *testing.T) {
	s, err := New("minio:9000", "media.example.test", "access", "secret", false, true)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.ObjectRef{Bucket: "imports", Object: "verified/video.mp4"}
	public, err := s.PresignGet(context.Background(), ref, 600*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(public)
	if err != nil || u.Scheme != "https" || u.Host != "media.example.test" || u.Query().Get("X-Amz-Expires") != "600" {
		t.Fatalf("incorrect public URL %v", u)
	}
	internal, err := s.PresignInternalPut(context.Background(), ref, 900*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u, err = url.Parse(internal)
	if err != nil || u.Scheme != "http" || u.Host != "minio:9000" || u.Query().Get("X-Amz-Expires") != "900" {
		t.Fatalf("incorrect internal URL %v", u)
	}
	legacy, err := New("minio:9000", "media.example.test", "access", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	legacyURL, err := legacy.PresignGet(context.Background(), ref, 900*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u, err = url.Parse(legacyURL)
	if err != nil || u.Scheme != "http" {
		t.Fatalf("legacy TLS fallback changed: %v", u)
	}
}
