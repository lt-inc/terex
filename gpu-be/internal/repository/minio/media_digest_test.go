package minio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
)

func TestMediaDigestUsesActualBytesAndVersionBoundedRead(t *testing.T) {
	for _, mode := range []string{"valid", "changed", "truncated", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			gets := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"fixture-etag"`)
				w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
				if r.Method == http.MethodHead {
					size := int64(3)
					if mode == "oversized" {
						size = workflow.MaxPublicationMediaBytes + 1
					}
					w.Header().Set("Content-Length", fmt.Sprint(size))
					return
				}
				gets++
				if r.Header.Get("If-Match") != `"fixture-etag"` {
					t.Error("GET is not pinned to HEAD object")
				}
				if mode == "changed" {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				body := "abc"
				if mode == "truncated" {
					body = "ab"
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			s, err := New(strings.TrimPrefix(srv.URL, "http://"), "media.example.test", "fixture", "fixture", false)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := s.MediaDigest(context.Background(), domain.ObjectRef{Bucket: "clips", Object: "ready.mp4"})
			if mode == "valid" {
				if err != nil || proof == nil || proof.SizeBytes != 3 || proof.SHA256 != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
					t.Fatal(proof, err)
				}
			} else if err == nil || proof != nil {
				t.Fatal("unsafe object accepted", proof, err)
			}
			if mode == "oversized" && gets != 0 {
				t.Fatal("oversized object was downloaded")
			}
		})
	}
}
