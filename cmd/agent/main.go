package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mini-modal/internal/agent"
	"mini-modal/internal/domain"
	"mini-modal/internal/executor"
	"mini-modal/internal/transport/agentgrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func main() {
	nodeID := flag.String("node-id", "", "ID of the node this agent represents")
	controlPlaneAddress := flag.String(
		"control-plane-address",
		"localhost:9090",
		"address of the control plane gRPC server",
	)
	gpuType := flag.String("gpu-type", "", "GPU model exposed by this node")
	totalGPUs := flag.Int64("gpus", 0, "number of GPUs exposed by this node")
	totalCPUs := flag.Int64("cpus", 8, "number of CPU cores exposed by this node")
	totalMemoryGB := flag.Int64("memory-gb", 16, "memory in GB exposed by this node")

	flag.Parse()
	*nodeID = strings.TrimSpace(*nodeID)
	if *nodeID == "" {
		log.Fatal("-node-id is required")
	}
	if *totalGPUs < 0 {
		log.Fatal("-gpus cannot be negative")
	}
	if *totalCPUs <= 0 {
		log.Fatal("-cpus must be greater than zero")
	}
	if *totalMemoryGB <= 0 {
		log.Fatal("-memory-gb must be greater than zero")
	}
	log.SetPrefix("agent " + *nodeID + ": ")

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	conn, err := grpc.DialContext(
		ctx,
		*controlPlaneAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("failed to connect to control plane: %v", err)
	}
	defer conn.Close()

	client := agentgrpc.NewClient(conn)
	node := domain.Node{
		ID:            *nodeID,
		GPUType:       strings.TrimSpace(*gpuType),
		TotalGPUs:     *totalGPUs,
		TotalCPUs:     *totalCPUs,
		TotalMemoryGB: *totalMemoryGB,
	}
	if err := client.RegisterNode(ctx, node); err != nil {
		log.Fatalf("failed to register node: %v", err)
	}

	log.Println("node registered")

	agent := agent.New(
		*nodeID,
		client,
		executor.ProcessExecutor{},
		250*time.Millisecond,
	)

	go agent.Run(ctx)

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := client.Heartbeat(ctx, *nodeID); err != nil {
					log.Printf("failed to heartbeat: %v", err)
					if status.Code(err) == codes.NotFound {
						if registerErr := client.RegisterNode(ctx, node); registerErr != nil {
							log.Printf("failed to re-register node: %v", registerErr)
						}
					}
				}
			}
		}
	}()

	<-ctx.Done()
	log.Println("agent stopped")
}
