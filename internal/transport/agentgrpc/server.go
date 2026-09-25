package agentgrpc

import (
	"context"
	"io"
	"strings"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/controlplane"
	"mini-modal/internal/domain"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	agentv1.UnimplementedAgentServiceServer
	service *controlplane.Service
}

func NewServer(service *controlplane.Service) *Server {
	return &Server{
		service: service,
	}
}

var _ agentv1.AgentServiceServer = (*Server)(nil)

func (s *Server) StreamLogs(stream grpc.ClientStreamingServer[agentv1.StreamLogsRequest, agentv1.StreamLogsResponse]) error {
	var (
		chunksReceived int64
		jobID          string
		attempt        int64
		lastSequence   int64
	)

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&agentv1.StreamLogsResponse{
				ChunksReceived: chunksReceived,
			})
		}
		if err != nil {
			return err
		}

		chunkJobID := strings.TrimSpace(chunk.GetJobId())
		if chunkJobID == "" {
			return status.Error(codes.InvalidArgument, "job ID is required")
		}
		if chunk.GetAttempt() <= 0 {
			return status.Error(codes.InvalidArgument, "attempt must be greater than 0")
		}
		if chunk.GetSequence() < 0 {
			return status.Error(codes.InvalidArgument, "sequence cannot be negative")
		}
		if chunk.GetSource() != agentv1.LogSource_LOG_SOURCE_STDOUT &&
			chunk.GetSource() != agentv1.LogSource_LOG_SOURCE_STDERR {
			return status.Error(codes.InvalidArgument, "log source must be stdout or stderr")
		}
		if len(chunk.GetData()) == 0 {
			return status.Error(codes.InvalidArgument, "log data cannot be empty")
		}
		if chunk.GetCreatedAtUnixMs() <= 0 {
			return status.Error(codes.InvalidArgument, "log creation time is required")
		}

		if chunksReceived == 0 {
			record, err := s.service.Job(stream.Context(), chunkJobID)
			if err != nil {
				return status.Errorf(codes.NotFound, "failed to find job: %v", err)
			}
			if record.Attempt != chunk.GetAttempt() {
				return status.Errorf(
					codes.FailedPrecondition,
					"job %q is on attempt %d, received logs for attempt %d",
					chunkJobID,
					record.Attempt,
					chunk.GetAttempt(),
				)
			}
			if record.Status != controlplane.JobRunning {
				return status.Errorf(
					codes.FailedPrecondition,
					"job %q is %q, not running",
					chunkJobID,
					record.Status,
				)
			}

			jobID = chunkJobID
			attempt = chunk.GetAttempt()
			lastSequence = chunk.GetSequence()
		} else {
			if chunkJobID != jobID || chunk.GetAttempt() != attempt {
				return status.Error(codes.InvalidArgument, "all chunks in a stream must belong to the same assignment")
			}
			if chunk.GetSequence() <= lastSequence {
				return status.Error(codes.InvalidArgument, "log sequence must increase")
			}
			lastSequence = chunk.GetSequence()
		}

		if err := s.service.CreateJobLog(stream.Context(), domain.LogStream{
			JobID:     jobID,
			Attempt:   attempt,
			Sequence:  lastSequence,
			Source:    chunk.GetSource(),
			Data:      chunk.GetData(),
			CreatedAt: time.UnixMilli(chunk.GetCreatedAtUnixMs()),
		}); err != nil {
			return status.Errorf(codes.Internal, "failed to create job log: %v", err)
		}

		chunksReceived++
	}
}

func (s *Server) Claim(ctx context.Context, req *agentv1.ClaimRequest) (*agentv1.ClaimResponse, error) {
	nodeID := strings.TrimSpace(req.GetNodeId())
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	assignment, found, err := s.service.Claim(ctx, nodeID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to claim job: %v", err)
	}
	if !found {
		return &agentv1.ClaimResponse{}, nil
	}

	return &agentv1.ClaimResponse{
		Assignment: &agentv1.Assignment{
			JobId:   assignment.JobID,
			NodeId:  assignment.NodeID,
			Attempt: assignment.Attempt,
			Command: assignment.Command,
			Args:    assignment.Args,
			Image:   assignment.Image,
		},
	}, nil
}

func (s *Server) Complete(ctx context.Context, req *agentv1.CompleteRequest) (*agentv1.CompleteResponse, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job ID is required")
	}

	attempt := req.GetAttempt()
	if attempt <= 0 {
		return nil, status.Error(codes.InvalidArgument, "attempt must be greater than 0")
	}

	if err := s.service.Complete(ctx, domain.Assignment{
		JobID:   jobID,
		Attempt: attempt,
	}); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to complete job: %v", err)
	}

	return &agentv1.CompleteResponse{}, nil
}

func (s *Server) RenewAssignment(ctx context.Context, req *agentv1.RenewAssignmentRequest) (*agentv1.RenewAssignmentResponse, error) {
	assignment := req.GetAssignment()
	if assignment == nil {
		return nil, status.Error(codes.InvalidArgument, "assignment is required")
	}
	if assignment.GetAttempt() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "attempt must be greater than 0")
	}
	if assignment.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}
	if assignment.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job ID is required")
	}

	if err := s.service.RenewAssignment(ctx, domain.Assignment{
		JobID:   assignment.GetJobId(),
		NodeID:  assignment.GetNodeId(),
		Attempt: assignment.GetAttempt(),
	}); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to renew lease: %v", err)
	}
	return &agentv1.RenewAssignmentResponse{}, nil
}

func (s *Server) Fail(ctx context.Context, req *agentv1.FailRequest) (*agentv1.FailResponse, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job ID is required")
	}
	attempt := req.GetAttempt()
	if attempt <= 0 {
		return nil, status.Error(codes.InvalidArgument, "attempt must be greater than 0")
	}

	if err := s.service.Fail(ctx, domain.Assignment{
		JobID:   jobID,
		Attempt: attempt,
	}); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to fail job: %v", err)
	}

	return &agentv1.FailResponse{}, nil
}

func (s *Server) RegisterNode(ctx context.Context, req *agentv1.RegisterNodeRequest) (*agentv1.RegisterNodeResponse, error) {
	nodeID := strings.TrimSpace(req.GetNodeId())
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	node := domain.Node{
		ID:            nodeID,
		GPUType:       strings.TrimSpace(req.GetGpuType()),
		TotalGPUs:     int64(req.GetTotalGpus()),
		TotalCPUs:     int64(req.GetTotalCpus()),
		TotalMemoryGB: int64(req.GetTotalMemoryGb()),
	}

	if err := s.service.RegisterNode(ctx, node); err != nil {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"failed to register node: %v",
			err,
		)
	}

	return &agentv1.RegisterNodeResponse{}, nil
}
func (s *Server) Heartbeat(_ context.Context, req *agentv1.HeartbeatRequest) (*agentv1.HeartbeatResponse, error) {
	nodeID := strings.TrimSpace(req.GetNodeId())
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	if err := s.service.Heartbeat(nodeID); err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to heartbeat: %v", err)
	}
	return &agentv1.HeartbeatResponse{}, nil
}
