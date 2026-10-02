// Package gpuclient is main-be's gRPC client to gpu-be (health + storage gateway).
package gpuclient

import (
	"context"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	gpuv1 "terex/proto/gpu/v1"

	"terex/main-be/internal/domain"
)

type Client struct {
	conn *grpc.ClientConn
	c    gpuv1.GpuServiceClient
}

// New dials lazily; gpu-be may start after main-be.
func New(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, c: gpuv1.NewGpuServiceClient(conn)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func rpc(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 15*time.Second)
}

func (c *Client) Health(ctx context.Context) error {
	ctx, cancel := rpc(ctx)
	defer cancel()
	_, err := c.c.Health(ctx, &gpuv1.HealthRequest{})
	return err
}

func (c *Client) PresignGet(ctx context.Context, bucket, object string) (string, error) {
	ctx, cancel := rpc(ctx)
	defer cancel()
	r, err := c.c.PresignGet(ctx, &gpuv1.PresignGetRequest{Object: &gpuv1.ObjectRef{Bucket: bucket, Object: object}, TtlSeconds: 3600})
	if err != nil {
		return "", err
	}
	return r.Url, nil
}

func (c *Client) PresignPut(ctx context.Context, kind domain.AssetKind, fileName string) (string, string, string, error) {
	k := gpuv1.AssetKind_ASSET_KIND_BANNER
	if kind == domain.KindBackground {
		k = gpuv1.AssetKind_ASSET_KIND_BACKGROUND
	}
	ctx, cancel := rpc(ctx)
	defer cancel()
	r, err := c.c.PresignPut(ctx, &gpuv1.PresignPutRequest{Kind: k, FileName: fileName, TtlSeconds: 900})
	if err != nil {
		return "", "", "", err
	}
	return r.Url, r.Object.Bucket, r.Object.Object, nil
}

func (c *Client) PutAuthFile(ctx context.Context, platform, account string, content []byte) (string, string, error) {
	ctx, cancel := rpc(ctx)
	defer cancel()
	r, err := c.c.PutAuthFile(ctx, &gpuv1.PutAuthFileRequest{Platform: platform, AccountName: account, Content: content})
	if err != nil {
		return "", "", err
	}
	return r.Bucket, r.Object, nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket, object string) error {
	ctx, cancel := rpc(ctx)
	defer cancel()
	_, err := c.c.DeleteObject(ctx, &gpuv1.ObjectRef{Bucket: bucket, Object: object})
	return err
}
