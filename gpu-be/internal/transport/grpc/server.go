// Package grpc exposes job state and the storage gateway to main-be.
package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	gpuv1 "terex/proto/gpu/v1"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/jobstate"
	"terex/gpu-be/internal/service/storagegw"
)

type Server struct {
	gpuv1.UnimplementedGpuServiceServer
	State domain.JobState
	GW    *storagegw.Service
}

func ref(r *gpuv1.ObjectRef) domain.ObjectRef {
	return domain.ObjectRef{Bucket: r.GetBucket(), Object: r.GetObject()}
}

func (s *Server) Health(context.Context, *gpuv1.HealthRequest) (*gpuv1.HealthResponse, error) {
	return &gpuv1.HealthResponse{Ok: true}, nil
}

func (s *Server) GetJobStatus(ctx context.Context, r *gpuv1.GetJobStatusRequest) (*gpuv1.GetJobStatusResponse, error) {
	st, at, err := s.State.GetStatus(ctx, r.JobId)
	if errors.Is(err, jobstate.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &gpuv1.GetJobStatusResponse{JobId: st.JobID, Stage: st.Stage, Status: st.Status,
		FailPlace: st.FailPlace, UpdatedAt: timestamppb.New(at)}, nil
}

func (s *Server) ListClips(ctx context.Context, r *gpuv1.ListClipsRequest) (*gpuv1.ListClipsResponse, error) {
	clips, err := s.State.ListClips(ctx, r.JobId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &gpuv1.ListClipsResponse{}
	for _, c := range clips {
		out.Clips = append(out.Clips, &gpuv1.Clip{
			ClipId: c.ClipID, JobId: c.JobID, StartSec: c.Start, EndSec: c.End, Title: c.Title, Hashtags: c.Hashtags,
			Object: &gpuv1.ObjectRef{Bucket: c.Object.Bucket, Object: c.Object.Object},
		})
	}
	return out, nil
}

func (s *Server) PresignGet(ctx context.Context, r *gpuv1.PresignGetRequest) (*gpuv1.PresignResponse, error) {
	u, err := s.GW.PresignGet(ctx, ref(r.Object), r.TtlSeconds)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &gpuv1.PresignResponse{Url: u, Object: r.Object}, nil
}

func (s *Server) PresignPut(ctx context.Context, r *gpuv1.PresignPutRequest) (*gpuv1.PresignResponse, error) {
	kind := map[gpuv1.AssetKind]string{gpuv1.AssetKind_ASSET_KIND_BANNER: "banner", gpuv1.AssetKind_ASSET_KIND_BACKGROUND: "background"}[r.Kind]
	u, o, err := s.GW.PresignPut(ctx, kind, r.FileName, r.TtlSeconds)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &gpuv1.PresignResponse{Url: u, Object: &gpuv1.ObjectRef{Bucket: o.Bucket, Object: o.Object}}, nil
}

func (s *Server) PutAuthFile(ctx context.Context, r *gpuv1.PutAuthFileRequest) (*gpuv1.ObjectRef, error) {
	o, err := s.GW.PutAuthFile(ctx, r.Platform, r.AccountName, r.Content)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &gpuv1.ObjectRef{Bucket: o.Bucket, Object: o.Object}, nil
}

func (s *Server) DeleteObject(ctx context.Context, r *gpuv1.ObjectRef) (*gpuv1.DeleteObjectResponse, error) {
	if err := s.GW.Delete(ctx, ref(r)); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &gpuv1.DeleteObjectResponse{}, nil
}
