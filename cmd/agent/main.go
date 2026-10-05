package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mini-modal/internal/agent"
	"mini-modal/internal/artifact"
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
	poolID := flag.String("pool-id", "", "node pool this agent belongs to")
	controlPlaneAddress := flag.String(
		"control-plane-address",
		"localhost:9090",
		"address of the control plane gRPC server",
	)
	gpuType := flag.String("gpu-type", "", "GPU model exposed by this node")
	totalGPUs := flag.Int64("gpus", 0, "number of GPUs exposed by this node")
	totalCPUs := flag.Int64("cpus", 8, "number of CPU cores exposed by this node")
	totalMemoryGB := flag.Int64("memory-gb", 16, "memory in GB exposed by this node")
	executorName := flag.String("executor", "fake", "executor implementation: fake, process, or docker")
	dockerBinary := flag.String("docker-binary", "docker", "Docker CLI path when using the docker executor")

	flag.Parse()
	*nodeID = strings.TrimSpace(*nodeID)
	if *nodeID == "" {
		log.Fatal("-node-id is required")
	}
	*poolID = strings.TrimSpace(*poolID)
	if *poolID == "" {
		log.Fatal("-pool-id is required")
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

	var artifactStore executor.ArtifactStore
	if strings.EqualFold(strings.TrimSpace(*executorName), "docker") || strings.EqualFold(strings.TrimSpace(*executorName), "process") {
		store, err := artifact.NewS3Store(ctx, artifact.Config{
			Endpoint:  os.Getenv("S3_ENDPOINT"),
			AccessKey: os.Getenv("S3_ACCESS_KEY_ID"),
			SecretKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
			Bucket:    os.Getenv("S3_BUCKET"),
		})
		if err != nil {
			log.Fatalf("failed to configure artifact storage: %v", err)
		}
		artifactStore = store
	}
	selectedExecutor, err := selectExecutor(*executorName, *dockerBinary, artifactStore)
	if err != nil {
		log.Fatal(err)
	}

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
		PoolID:        *poolID,
		GPUType:       strings.TrimSpace(*gpuType),
		TotalGPUs:     *totalGPUs,
		TotalCPUs:     *totalCPUs,
		TotalMemoryGB: *totalMemoryGB,
	}
	shouldDrain, err := client.RegisterNode(ctx, node)
	if err != nil {
		log.Fatalf("failed to register node: %v", err)
	}

	log.Println("node registered")
	agent := agent.New(
		*nodeID,
		client,
		selectedExecutor,
		agent.DefaultClaimInterval,
	)
	drainContext, drainCancel := context.WithCancel(ctx)
	defer drainCancel()
	requestDrain := func() {
		if drainContext.Err() == nil {
			log.Println("node should drain")
			drainCancel()
		}
	}
	if shouldDrain {
		requestDrain()
	}

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				shouldDrain, err := client.Heartbeat(ctx, *nodeID)
				if err != nil {
					log.Printf("failed to heartbeat: %v", err)
					if status.Code(err) == codes.NotFound {
						registeredShouldDrain, registerErr := client.RegisterNode(ctx, node)
						if registerErr != nil {
							log.Printf("failed to re-register node: %v", registerErr)
						} else if registeredShouldDrain {
							requestDrain()
						}
					}
				}
				if shouldDrain {
					requestDrain()
				}
			}
		}
	}()
	agent.Run(ctx, drainContext)

	log.Println("agent stopped")
}

func selectExecutor(name, dockerBinary string, artifactStore executor.ArtifactStore) (executor.Executor, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "fake":
		return executor.Fake{}, nil
	case "process":
		return executor.ProcessExecutor{Artifacts: artifactStore}, nil
	case "docker":
		return executor.DockerExecutor{Binary: strings.TrimSpace(dockerBinary), Artifacts: artifactStore}, nil
	default:
		return nil, fmt.Errorf("unknown executor %q: use fake, process, or docker", name)
	}
}
