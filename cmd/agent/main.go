package main

import (
	"context"
	"flag"
	"log"
	"mini-modal/internal/agent"
	"mini-modal/internal/domain"
	"mini-modal/internal/executor"
	"mini-modal/internal/transport/agentgrpc"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	flag.Parse()
	if *nodeID == "" {
		log.Fatal("-node-id is required")
	}

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
		GPUType:       "A100",
		TotalGPUs:     4,
		TotalCPUs:     64,
		TotalMemoryGB: 256,
	}
	if err := client.RegisterNode(ctx, node); err != nil {
		log.Fatalf("failed to register node: %v", err)
	}

	log.Println("node registered")

	agent := agent.New(
		*nodeID,
		client,
		executor.Fake{},
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
