package agentgrpc

import (
	"context"

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
		JobID:   assignment.GetJobId(),
		NodeID:  assignment.GetNodeId(),
		Attempt: assignment.GetAttempt(),
	}, true, nil
}

func (c *Client) Complete(ctx context.Context, assignment domain.Assignment) error {
	_, err := c.client.Complete(ctx, &agentv1.CompleteRequest{
		JobId:   assignment.JobID,
		Attempt: assignment.Attempt,
	})
	return err
}

func (c *Client) RenewAssignment(ctx context.Context, assignment domain.Assignment) error {
	_, err := c.client.RenewAssignment(ctx, &agentv1.RenewAssignmentRequest{
		Assignment: &agentv1.Assignment{
			JobId:   assignment.JobID,
			NodeId:  assignment.NodeID,
			Attempt: assignment.Attempt,
		},
	})
	return err
}

func (c *Client) Fail(ctx context.Context, assignment domain.Assignment) error {
	_, err := c.client.Fail(ctx, &agentv1.FailRequest{
		JobId:   assignment.JobID,
		Attempt: assignment.Attempt,
	})
	return err
}

func (c *Client) RegisterNode(ctx context.Context, node domain.Node) error {
	_, err := c.client.RegisterNode(ctx, &agentv1.RegisterNodeRequest{
		NodeId:        node.ID,
		GpuType:       node.GPUType,
		TotalGpus:     int32(node.TotalGPUs),
		TotalCpus:     int32(node.TotalCPUs),
		TotalMemoryGb: int32(node.TotalMemoryGB),
	})
	return err
}

func (c *Client) Heartbeat(ctx context.Context, nodeID string) error {
	_, err := c.client.Heartbeat(ctx, &agentv1.HeartbeatRequest{
		NodeId: nodeID,
	})
	return err
}
