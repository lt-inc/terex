package workflow

import (
	"context"
	"encoding/json"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"strings"
	"time"
)

type Coordinator interface {
	Claim(context.Context, *ClaimRequest) (*Lease, error)
	Renew(context.Context, *LeaseRequest) (*Permit, error)
	Checkpoint(context.Context, *CheckpointRequest) (*Result, error)
	Complete(context.Context, *CompleteRequest) (*Result, error)
	Fail(context.Context, *FailRequest) (*Result, error)
}
type Gateway interface {
	VerifyPublicationMedia(context.Context, *VerifyPublicationMediaRequest) (*MediaDigest, error)
	OAuthURL(context.Context, *OAuthURLRequest) (*OAuthURLResponse, error)
	OAuthExchange(context.Context, *OAuthExchangeRequest) (*Connection, error)
	CheckConnection(context.Context, *ConnectionRequest) (*Connection, error)
	Disconnect(context.Context, *ConnectionRequest) (*Result, error)
	EditorContext(context.Context, *EditorContextRequest) (*EditorContextResponse, error)
	ChannelStats(context.Context, *ChannelStatsRequest) (*ChannelStatsResponse, error)
	PublicVideos(context.Context, *PublicVideosRequest) (*PublicVideosResponse, error)
	ImportBackground(context.Context, *ImportBackgroundRequest) (*ImportBackgroundResponse, error)
	WatermarkFrames(context.Context, *WatermarkFramesRequest) (*WatermarkFramesResponse, error)
}

type coordinatorClient struct{ conn grpc.ClientConnInterface }
type gatewayClient struct{ conn grpc.ClientConnInterface }

func NewCoordinatorClient(conn grpc.ClientConnInterface) Coordinator { return &coordinatorClient{conn} }
func NewGatewayClient(conn grpc.ClientConnInterface) Gateway         { return &gatewayClient{conn} }

func call[Q, R any](ctx context.Context, conn grpc.ClientConnInterface, method string, req *Q) (*R, error) {
	timeout := 15 * time.Second
	if strings.HasSuffix(method, "/VerifyUpload") || strings.HasSuffix(method, "/VerifyPublicationMedia") {
		timeout = 5 * time.Minute
	}
	if strings.HasSuffix(method, "/ChannelStats") {
		timeout = 2 * time.Minute
	}
	if strings.HasSuffix(method, "/PublicVideos") {
		timeout = time.Minute
	}
	if strings.HasSuffix(method, "/ImportBackground") {
		timeout = 10 * time.Minute
	}
	if strings.HasSuffix(method, "/WatermarkFrames") {
		timeout = 5 * time.Minute
	}
	if strings.HasSuffix(method, "/EditorContext") {
		timeout = 2 * time.Minute
	}
	if strings.HasSuffix(method, "/OAuthExchange") {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := &wrapperspb.BytesValue{}
	if err = conn.Invoke(ctx, method, &wrapperspb.BytesValue{Value: b}, out); err != nil {
		return nil, err
	}
	r := new(R)
	if err = json.Unmarshal(out.Value, r); err != nil {
		return nil, status.Error(codes.Internal, "invalid workflow response")
	}
	return r, nil
}
func (c *coordinatorClient) Claim(ctx context.Context, q *ClaimRequest) (*Lease, error) {
	return call[ClaimRequest, Lease](ctx, c.conn, "/terex.workflow.v1.Coordinator/Claim", q)
}
func (c *coordinatorClient) Renew(ctx context.Context, q *LeaseRequest) (*Permit, error) {
	return call[LeaseRequest, Permit](ctx, c.conn, "/terex.workflow.v1.Coordinator/Renew", q)
}
func (c *coordinatorClient) Checkpoint(ctx context.Context, q *CheckpointRequest) (*Result, error) {
	return call[CheckpointRequest, Result](ctx, c.conn, "/terex.workflow.v1.Coordinator/Checkpoint", q)
}
func (c *coordinatorClient) Complete(ctx context.Context, q *CompleteRequest) (*Result, error) {
	return call[CompleteRequest, Result](ctx, c.conn, "/terex.workflow.v1.Coordinator/Complete", q)
}
func (c *coordinatorClient) Fail(ctx context.Context, q *FailRequest) (*Result, error) {
	return call[FailRequest, Result](ctx, c.conn, "/terex.workflow.v1.Coordinator/Fail", q)
}
func (c *gatewayClient) OAuthURL(ctx context.Context, q *OAuthURLRequest) (*OAuthURLResponse, error) {
	return call[OAuthURLRequest, OAuthURLResponse](ctx, c.conn, "/terex.workflow.v1.Gateway/OAuthURL", q)
}
func (c *gatewayClient) VerifyPublicationMedia(ctx context.Context, q *VerifyPublicationMediaRequest) (*MediaDigest, error) {
	return call[VerifyPublicationMediaRequest, MediaDigest](ctx, c.conn, "/terex.workflow.v1.Gateway/VerifyPublicationMedia", q)
}
func (c *gatewayClient) OAuthExchange(ctx context.Context, q *OAuthExchangeRequest) (*Connection, error) {
	return call[OAuthExchangeRequest, Connection](ctx, c.conn, "/terex.workflow.v1.Gateway/OAuthExchange", q)
}
func (c *gatewayClient) CheckConnection(ctx context.Context, q *ConnectionRequest) (*Connection, error) {
	return call[ConnectionRequest, Connection](ctx, c.conn, "/terex.workflow.v1.Gateway/CheckConnection", q)
}
func (c *gatewayClient) Disconnect(ctx context.Context, q *ConnectionRequest) (*Result, error) {
	return call[ConnectionRequest, Result](ctx, c.conn, "/terex.workflow.v1.Gateway/Disconnect", q)
}
func (c *gatewayClient) EditorContext(ctx context.Context, q *EditorContextRequest) (*EditorContextResponse, error) {
	return call[EditorContextRequest, EditorContextResponse](ctx, c.conn, "/terex.workflow.v1.Gateway/EditorContext", q)
}
func (c *gatewayClient) ChannelStats(ctx context.Context, q *ChannelStatsRequest) (*ChannelStatsResponse, error) {
	return call[ChannelStatsRequest, ChannelStatsResponse](ctx, c.conn, "/terex.workflow.v1.Gateway/ChannelStats", q)
}

func (c *gatewayClient) PublicVideos(ctx context.Context, q *PublicVideosRequest) (*PublicVideosResponse, error) {
	return call[PublicVideosRequest, PublicVideosResponse](ctx, c.conn, "/terex.workflow.v1.Gateway/PublicVideos", q)
}

func (c *gatewayClient) WatermarkFrames(ctx context.Context, q *WatermarkFramesRequest) (*WatermarkFramesResponse, error) {
	return call[WatermarkFramesRequest, WatermarkFramesResponse](ctx, c.conn, "/terex.workflow.v1.Gateway/WatermarkFrames", q)
}
func (c *gatewayClient) ImportBackground(ctx context.Context, q *ImportBackgroundRequest) (*ImportBackgroundResponse, error) {
	return call[ImportBackgroundRequest, ImportBackgroundResponse](ctx, c.conn, "/terex.workflow.v1.Gateway/ImportBackground", q)
}

// method is the same unary dispatch used by generated gRPC code; the wire
// message remains protobuf, and domain payload validation belongs to services.
func method[S, Q, R any](service, name string, invoke func(S, context.Context, *Q) (*R, error)) grpc.MethodDesc {
	return grpc.MethodDesc{MethodName: name, Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		wire := &wrapperspb.BytesValue{}
		if err := dec(wire); err != nil {
			return nil, err
		}
		q := new(Q)
		if err := json.Unmarshal(wire.Value, q); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid workflow payload")
		}
		h := func(ctx context.Context, _ any) (any, error) {
			r, err := invoke(srv.(S), ctx, q)
			if err != nil {
				return nil, err
			}
			b, err := json.Marshal(r)
			if err != nil {
				return nil, status.Error(codes.Internal, "cannot encode workflow response")
			}
			return &wrapperspb.BytesValue{Value: b}, nil
		}
		if interceptor == nil {
			return h(ctx, q)
		}
		return interceptor(ctx, q, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + service + "/" + name}, h)
	}}
}
func RegisterCoordinatorServer(reg grpc.ServiceRegistrar, srv Coordinator) {
	const s = "terex.workflow.v1.Coordinator"
	reg.RegisterService(&grpc.ServiceDesc{ServiceName: s, HandlerType: (*Coordinator)(nil), Methods: []grpc.MethodDesc{
		method(s, "Claim", Coordinator.Claim), method(s, "Renew", Coordinator.Renew), method(s, "Checkpoint", Coordinator.Checkpoint), method(s, "Complete", Coordinator.Complete), method(s, "Fail", Coordinator.Fail),
	}, Metadata: "workflow/workflow.proto"}, srv)
}
func RegisterGatewayServer(reg grpc.ServiceRegistrar, srv Gateway) {
	const s = "terex.workflow.v1.Gateway"
	reg.RegisterService(&grpc.ServiceDesc{ServiceName: s, HandlerType: (*Gateway)(nil), Methods: []grpc.MethodDesc{
		method(s, "VerifyPublicationMedia", Gateway.VerifyPublicationMedia),
		method(s, "OAuthURL", Gateway.OAuthURL), method(s, "OAuthExchange", Gateway.OAuthExchange), method(s, "CheckConnection", Gateway.CheckConnection), method(s, "Disconnect", Gateway.Disconnect), method(s, "EditorContext", Gateway.EditorContext), method(s, "ChannelStats", Gateway.ChannelStats), method(s, "PublicVideos", Gateway.PublicVideos), method(s, "ImportBackground", Gateway.ImportBackground), method(s, "WatermarkFrames", Gateway.WatermarkFrames),
	}, Metadata: "workflow/workflow.proto"}, srv)
}
