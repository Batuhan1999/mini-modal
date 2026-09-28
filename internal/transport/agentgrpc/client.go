package agentgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/domain"

	"google.golang.org/grpc"
)

type Client struct {
	client agentv1.AgentServiceClient
}

func NewClient(connection grpc.ClientConnInterface) *Client {
	return &Client{
		client: agentv1.NewAgentServiceClient(connection),
	}
}

func (c *Client) StreamLogs(
	ctx context.Context,
	chunks <-chan domain.LogStream,
) error {
	stream, err := c.client.StreamLogs(ctx)
	if err != nil {
		return fmt.Errorf("open log stream: %w", err)
	}

	var sent int64
	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return ctx.Err()

		case chunk, ok := <-chunks:
			if !ok {
				response, err := stream.CloseAndRecv()
				if err != nil {
					return fmt.Errorf("close log stream: %w", err)
				}
				if response.GetChunksReceived() != sent {
					return fmt.Errorf(
						"control plane received %d log chunks, sent %d",
						response.GetChunksReceived(),
						sent,
					)
				}
				return nil
			}

			if err := stream.Send(&agentv1.StreamLogsRequest{
				JobId:           chunk.JobID,
				Attempt:         chunk.Attempt,
				Sequence:        chunk.Sequence,
				Source:          chunk.Source,
				Data:            chunk.Data,
				CreatedAtUnixMs: chunk.CreatedAt.UnixMilli(),
			}); err != nil {
				return fmt.Errorf("send log chunk %d for job %q: %w", chunk.Sequence, chunk.JobID, err)
			}
			sent++
		}
	}
}

func (c *Client) Claim(ctx context.Context, nodeID string) (domain.Assignment, bool, error) {
	response, err := c.client.Claim(ctx, &agentv1.ClaimRequest{
		NodeId: nodeID,
	})
	if err != nil {
		return domain.Assignment{}, false, err
	}

	assignment := response.GetAssignment()
	if assignment == nil {
		return domain.Assignment{}, false, nil
	}

	return domain.Assignment{
		JobID:          assignment.GetJobId(),
		NodeID:         assignment.GetNodeId(),
		Attempt:        assignment.GetAttempt(),
		Entrypoint:     assignment.GetEntrypoint(),
		InputJSON:      assignment.GetInputJson(),
		Image:          assignment.GetImage(),
		CPUs:           assignment.GetCpus(),
		MemoryGB:       assignment.GetMemoryGb(),
		TimeoutSeconds: assignment.GetTimeoutSeconds(),
		ArtifactKey:    assignment.GetArtifactKey(),
	}, true, nil
}

func (c *Client) Complete(ctx context.Context, assignment domain.Assignment, output json.RawMessage) error {
	_, err := c.client.Complete(ctx, &agentv1.CompleteRequest{
		JobId:      assignment.JobID,
		Attempt:    assignment.Attempt,
		OutputJson: output,
	})
	return err
}

func (c *Client) RenewAssignment(ctx context.Context, assignment domain.Assignment) (bool, error) {
	response, err := c.client.RenewAssignment(ctx, &agentv1.RenewAssignmentRequest{
		Assignment: &agentv1.Assignment{
			JobId:   assignment.JobID,
			NodeId:  assignment.NodeID,
			Attempt: assignment.Attempt,
		},
	})
	if err != nil {
		return false, err
	}
	return response.GetShouldCancel(), nil
}

func (c *Client) Cancel(ctx context.Context, assignment domain.Assignment) error {
	_, err := c.client.Cancel(ctx, &agentv1.CancelRequest{
		JobId:   assignment.JobID,
		Attempt: assignment.Attempt,
	})
	return err
}

func (c *Client) Fail(
	ctx context.Context,
	assignment domain.Assignment,
	reason domain.FailureReason,
	lastError string,
) error {
	_, err := c.client.Fail(ctx, &agentv1.FailRequest{
		JobId:     assignment.JobID,
		Attempt:   assignment.Attempt,
		LastError: lastError,
		Reason:    failureReasonToProto(reason),
	})
	return err
}

func failureReasonToProto(reason domain.FailureReason) agentv1.FailureReason {
	switch reason {
	case domain.FailureExecutionError:
		return agentv1.FailureReason_FAILURE_REASON_EXECUTION_ERROR
	case domain.FailureTimeout:
		return agentv1.FailureReason_FAILURE_REASON_TIMEOUT
	default:
		return agentv1.FailureReason_FAILURE_REASON_UNSPECIFIED
	}
}

func (c *Client) RegisterNode(ctx context.Context, node domain.Node) (bool, error) {
	response, err := c.client.RegisterNode(ctx, &agentv1.RegisterNodeRequest{
		NodeId:        node.ID,
		GpuType:       node.GPUType,
		TotalGpus:     int32(node.TotalGPUs),
		TotalCpus:     int32(node.TotalCPUs),
		TotalMemoryGb: int32(node.TotalMemoryGB),
	})
	if err != nil {
		return false, err
	}
	return response.GetShouldDrain(), nil
}

func (c *Client) Heartbeat(ctx context.Context, nodeID string) (bool, error) {
	response, err := c.client.Heartbeat(ctx, &agentv1.HeartbeatRequest{
		NodeId: nodeID,
	})
	return response.GetShouldDrain(), err
}
