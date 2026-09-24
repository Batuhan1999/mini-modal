package main

import (
	"context"
	"errors"
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
	"mini-modal/internal/store/sqlite"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
	"mini-modal/internal/transport/agentgrpc"
	"mini-modal/internal/transport/httpapi"

	"google.golang.org/grpc"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sqlite.Open(ctx, "data/mini-modal.db")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	queries := sqlitedb.New(db)
	service := controlplane.NewService(controlplane.NewRuntimeState(), queries)
	if err := service.Initialize(ctx); err != nil {
		log.Fatalf("failed to initialize control plane: %v", err)
	}
	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           httpapi.NewServer(service),
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
