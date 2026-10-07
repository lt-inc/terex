package workflow

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

// StorageInventory is restricted to Twitch-owned object prefixes. It deliberately
// cannot enumerate credentials, assets, or the legacy YouTube pipeline.
type StorageInventoryRequest struct {
	Bucket string `json:"bucket"`
	Prefix string `json:"prefix"`
	After  string `json:"after,omitempty"`
	Limit  int    `json:"limit"`
}
type StorageObject struct {
	Ref        ObjectRef `json:"ref"`
	SizeBytes  int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`
}
type StorageInventoryResponse struct {
	Objects []StorageObject `json:"objects"`
	Next    string          `json:"next,omitempty"`
}
type StorageDeleteRequest struct {
	Ref ObjectRef `json:"ref"`
}
type StorageGateway interface {
	Inventory(context.Context, *StorageInventoryRequest) (*StorageInventoryResponse, error)
	DeleteTwitchObject(context.Context, *StorageDeleteRequest) (*Result, error)
}
type storageClient struct{ conn grpc.ClientConnInterface }

func NewStorageGatewayClient(conn grpc.ClientConnInterface) StorageGateway {
	return &storageClient{conn}
}
func (c *storageClient) Inventory(ctx context.Context, q *StorageInventoryRequest) (*StorageInventoryResponse, error) {
	return call[StorageInventoryRequest, StorageInventoryResponse](ctx, c.conn, "/terex.workflow.v1.StorageGateway/Inventory", q)
}
func (c *storageClient) DeleteTwitchObject(ctx context.Context, q *StorageDeleteRequest) (*Result, error) {
	return call[StorageDeleteRequest, Result](ctx, c.conn, "/terex.workflow.v1.StorageGateway/DeleteTwitchObject", q)
}
func RegisterStorageGatewayServer(reg grpc.ServiceRegistrar, srv StorageGateway) {
	const s = "terex.workflow.v1.StorageGateway"
	reg.RegisterService(&grpc.ServiceDesc{ServiceName: s, HandlerType: (*StorageGateway)(nil), Methods: []grpc.MethodDesc{
		method(s, "Inventory", StorageGateway.Inventory), method(s, "DeleteTwitchObject", StorageGateway.DeleteTwitchObject),
	}, Metadata: "workflow/workflow.proto"}, srv)
}
