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
	"mini-modal/internal/controlplane"
	"mini-modal/internal/devcluster"
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
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("load .env: %v", err)
	}
	databaseURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection URL")
	dbMaxOpenConns := flag.Int("db-max-open-conns", 20, "maximum number of open Postgres connections")
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
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	queries := postgresdb.New(db)
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewDBStatsCollector(db, "mini-modal"),
	)
	metrics := controlplane.NewMetrics(registry)
	service := controlplane.NewService(controlplane.NewRuntimeState(), queries, db, metrics)
	if err := service.Initialize(ctx); err != nil {
		log.Fatalf("failed to initialize control plane: %v", err)
	}
	localAgents := devcluster.New(ctx, *agentBinary, *agentControlPlaneAddress)
	defer localAgents.Stop()
	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           httpapi.NewServer(service, localAgents, registry),
		ReadHeaderTimeout: 5 * time.Second,
	}

	grpcListener, err := net.Listen("tcp", ":9090")
	if err != nil {
		fmt.Println("failed to listen for gRPC:", err)
		return
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

	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		fmt.Println("server error:", err)
		stop()
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownContext); err != nil {
		fmt.Println("HTTP server shutdown error:", err)
	}

}
