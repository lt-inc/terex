package workflow

import (
	"context"
	"google.golang.org/grpc"
)

type TwitchMaintenanceRequest struct {
	Epoch  int64 `json:"epoch"`
	Active bool  `json:"active"`
}
type TwitchMaintenanceResult struct {
	Drained       bool `json:"drained"`
	ActiveWriters int  `json:"active_writers"`
}

// ResetGateway only controls local Twitch writers. It never deletes remote videos.
type ResetGateway interface {
	SetTwitchMaintenance(context.Context, *TwitchMaintenanceRequest) (*TwitchMaintenanceResult, error)
}
type resetClient struct{ conn grpc.ClientConnInterface }

func NewResetGatewayClient(conn grpc.ClientConnInterface) ResetGateway { return &resetClient{conn} }
func (c *resetClient) SetTwitchMaintenance(ctx context.Context, q *TwitchMaintenanceRequest) (*TwitchMaintenanceResult, error) {
	return call[TwitchMaintenanceRequest, TwitchMaintenanceResult](ctx, c.conn, "/terex.workflow.v1.ResetGateway/SetTwitchMaintenance", q)
}
func RegisterResetGatewayServer(reg grpc.ServiceRegistrar, srv ResetGateway) {
	const s = "terex.workflow.v1.ResetGateway"
	reg.RegisterService(&grpc.ServiceDesc{ServiceName: s, HandlerType: (*ResetGateway)(nil), Methods: []grpc.MethodDesc{method(s, "SetTwitchMaintenance", ResetGateway.SetTwitchMaintenance)}, Metadata: "workflow/workflow.proto"}, srv)
}
