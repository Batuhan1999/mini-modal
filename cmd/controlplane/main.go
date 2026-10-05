package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/artifact"
	"mini-modal/internal/capacity"
	"mini-modal/internal/controlplane"
	"mini-modal/internal/provisioner"
	"mini-modal/internal/store/postgres"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"mini-modal/internal/transport/agentgrpc"
	"mini-modal/internal/transport/httpapi"

	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	databaseURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection URL")
	dbMaxOpenConns := flag.Int("db-max-open-conns", 20, "maximum number of open Postgres connections")
	autoProvision := flag.Bool("auto-provision", true, "run the local capacity manager and provisioner")
	agentBinary := flag.String("agent-binary", "tmp/agent", "path to the local development agent binary")
	agentControlPlaneAddress := flag.String(
		"agent-control-plane-address",
		"localhost:9090",
		"gRPC address passed to locally launched development agents",
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, *databaseURL, *dbMaxOpenConns)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	queries := postgresdb.New(db)
	if *autoProvision {
		if err := queries.EnsureLocalNodePool(ctx); err != nil {
			return fmt.Errorf("initialize local node pool: %w", err)
		}
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewDBStatsCollector(db, "mini-modal"),
	)
	metrics := controlplane.NewMetrics(registry)
	artifactStore, err := artifact.NewS3Store(ctx, artifact.Config{
		Endpoint:  os.Getenv("S3_ENDPOINT"),
		AccessKey: os.Getenv("S3_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		Bucket:    os.Getenv("S3_BUCKET"),
	})
	if err != nil {
		return fmt.Errorf("configure artifact storage: %w", err)
	}
	service := controlplane.NewService(controlplane.NewRuntimeState(), queries, db, metrics, artifactStore)
	if err := service.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize control plane: %w", err)
	}
	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           httpapi.NewServer(service, registry),
		ReadHeaderTimeout: 5 * time.Second,
	}

	grpcListener, err := net.Listen("tcp", ":9090")
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	grpcServer := grpc.NewServer()
	defer grpcServer.GracefulStop()
	agentv1.RegisterAgentServiceServer(
		grpcServer,
		agentgrpc.NewServer(service),
	)

	serverErrors := make(chan error, 2)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil {
			serverErrors <- fmt.Errorf("gRPC server: %w", err)
		}
	}()

	go service.Run(ctx)
	var provisionerDone chan struct{}
	if *autoProvision {
		localAgents := provisioner.New(ctx, *agentBinary, *agentControlPlaneAddress, queries, service)
		capacityManager := capacity.New(queries, service.NodeHealthy)
		go capacityManager.Run(ctx)
		provisionerDone = make(chan struct{})
		go func() {
			defer close(provisionerDone)
			localAgents.Run(ctx)
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		runErr = err
		stop()
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownContext); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("shut down HTTP server: %w", err))
	}
	if provisionerDone != nil {
		<-provisionerDone
	}
	return runErr
}
