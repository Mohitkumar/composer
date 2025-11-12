package grpc

import (
	"fmt"
	"net"

	"github.com/mohitkumar/composer/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Server implements gRPC API for worker-coordinator communication
type Server struct {
	grpcServer  *grpc.Server
	config      *api.APIConfig
	coordinator interface{} // Will be *coord.Coordinator when handlers are implemented
}

// NewServer creates a new gRPC server for worker communication
func NewServer(config *api.APIConfig, coordinator interface{}) *Server {
	s := grpc.NewServer()

	// Register gRPC services
	// TODO: Register workflow service when proto is generated
	// workflowpb.RegisterWorkflowServiceServer(s, NewWorkflowService(coordinator))

	// Enable reflection for development/testing
	reflection.Register(s)

	return &Server{
		grpcServer:  s,
		config:      config,
		coordinator: coordinator,
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.config.GRPCPort))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

// Stop gracefully stops the gRPC server
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}

// GetServer returns the underlying gRPC server for service registration
func (s *Server) GetServer() *grpc.Server {
	return s.grpcServer
}
