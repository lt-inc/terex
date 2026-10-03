package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"terex/gpu-be/internal/domain"
)

type Store interface {
	Read(context.Context, domain.ObjectRef) ([]byte, error)
	Put(context.Context, domain.ObjectRef, io.Reader, int64) error
	Delete(context.Context, domain.ObjectRef) error
}

type Repository struct{ Store Store }

func Ref(accountID string) domain.ObjectRef {
	return domain.ObjectRef{Bucket: domain.BucketCredentials, Object: "google/" + accountID + ".json"}
}
func validate(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return fmt.Errorf("invalid credential id")
	}
	return nil
}
func (r *Repository) Get(ctx context.Context, id string) (domain.GoogleCredential, error) {
	if err := validate(id); err != nil {
		return domain.GoogleCredential{}, err
	}
	b, err := r.Store.Read(ctx, Ref(id))
	if err != nil {
		return domain.GoogleCredential{}, err
	}
	var c domain.GoogleCredential
	err = json.Unmarshal(b, &c)
	if err == nil && c.AccountID != id {
		err = fmt.Errorf("credential identity mismatch")
	}
	return c, err
}
func (r *Repository) Put(ctx context.Context, c domain.GoogleCredential) error {
	if err := validate(c.AccountID); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return r.Store.Put(ctx, Ref(c.AccountID), bytes.NewReader(b), int64(len(b)))
}
func (r *Repository) Delete(ctx context.Context, id string) error {
	if err := validate(id); err != nil {
		return err
	}
	return r.Store.Delete(ctx, Ref(id))
}

func ID(ref domain.ObjectRef) (string, error) {
	if ref.Bucket != domain.BucketCredentials || !strings.HasPrefix(ref.Object, "google/") || !strings.HasSuffix(ref.Object, ".json") {
		return "", fmt.Errorf("invalid credential reference")
	}
	id := strings.TrimSuffix(strings.TrimPrefix(ref.Object, "google/"), ".json")
	return id, validate(id)
}
