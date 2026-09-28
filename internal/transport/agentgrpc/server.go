package agentgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/controlplane"
	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"

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

func assignmentRPCError(action string, err error) error {
	if errors.Is(err, controlplane.ErrStaleAssignment) {
		return status.Errorf(codes.FailedPrecondition, "failed to %s job: %v", action, err)
	}
	return status.Errorf(codes.Unavailable, "failed to %s job: %v", action, err)
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
			chunk.GetSource() != agentv1.LogSource_LOG_SOURCE_STDERR &&
			chunk.GetSource() != agentv1.LogSource_LOG_SOURCE_SYSTEM {
			return status.Error(codes.InvalidArgument, "log source must be stdout, stderr, or system")
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
			jobStatus, err := jobmodel.ParseStatus(record.Status)
			if err != nil {
				return status.Errorf(codes.Internal, "job %q has invalid status: %v", chunkJobID, err)
			}
			if !jobStatus.AcceptsLogs() {
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
			JobId:          assignment.JobID,
			NodeId:         assignment.NodeID,
			Attempt:        assignment.Attempt,
			Entrypoint:     assignment.Entrypoint,
			InputJson:      assignment.InputJSON,
			Image:          assignment.Image,
			Cpus:           assignment.CPUs,
			MemoryGb:       assignment.MemoryGB,
			TimeoutSeconds: assignment.TimeoutSeconds,
			ArtifactKey:    assignment.ArtifactKey,
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
	}, req.GetOutputJson()); err != nil {
		return nil, assignmentRPCError("complete", err)
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

	shouldCancel, err := s.service.RenewAssignment(ctx, domain.Assignment{
		JobID:   assignment.GetJobId(),
		NodeID:  assignment.GetNodeId(),
		Attempt: assignment.GetAttempt(),
	})
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to renew lease: %v", err)
	}

	return &agentv1.RenewAssignmentResponse{ShouldCancel: shouldCancel}, nil

}

func (s *Server) Cancel(ctx context.Context, req *agentv1.CancelRequest) (*agentv1.CancelResponse, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job ID is required")
	}
	attempt := req.GetAttempt()
	if attempt <= 0 {
		return nil, status.Error(codes.InvalidArgument, "attempt must be greater than 0")
	}
	err := s.service.Cancel(ctx, domain.Assignment{JobID: jobID, Attempt: attempt})
	if err != nil {
		return nil, assignmentRPCError("cancel", err)
	}
	return &agentv1.CancelResponse{}, nil
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
	reason, err := failureReasonFromProto(req.GetReason())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := s.service.Fail(ctx, domain.Assignment{
		JobID:   jobID,
		Attempt: attempt,
	}, reason, req.GetLastError()); err != nil {
		return nil, assignmentRPCError("fail", err)
	}

	return &agentv1.FailResponse{}, nil
}

func failureReasonFromProto(reason agentv1.FailureReason) (domain.FailureReason, error) {
	switch reason {
	case agentv1.FailureReason_FAILURE_REASON_EXECUTION_ERROR:
		return domain.FailureExecutionError, nil
	case agentv1.FailureReason_FAILURE_REASON_TIMEOUT:
		return domain.FailureTimeout, nil
	default:
		return "", fmt.Errorf("failure reason is required")
	}
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

	shouldDrain, err := s.service.RegisterNode(ctx, node)
	if err != nil {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"failed to register node: %v",
			err,
		)
	}

	return &agentv1.RegisterNodeResponse{ShouldDrain: shouldDrain}, nil
}
func (s *Server) Heartbeat(ctx context.Context, req *agentv1.HeartbeatRequest) (*agentv1.HeartbeatResponse, error) {
	nodeID := strings.TrimSpace(req.GetNodeId())
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	shouldDrain, err := s.service.Heartbeat(ctx, nodeID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to heartbeat: %v", err)
	}
	return &agentv1.HeartbeatResponse{ShouldDrain: shouldDrain}, nil
}
