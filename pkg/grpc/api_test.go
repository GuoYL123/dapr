// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	daprv1pb "github.com/dapr/dapr/pkg/proto/dapr/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opencensus.io/trace"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	grpc_go "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxGRPCServerUptime = 100 * time.Millisecond

type mockGRPCAPI struct {
}

func (m *mockGRPCAPI) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var resp = invokev1.NewInvokeMethodResponse(0, "", nil)
	resp.WithRawData(ExtractSpanContext(ctx), "text/plains")
	return resp.Proto(), nil
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var resp = invokev1.NewInvokeMethodResponse(0, "", nil)
	resp.WithRawData(ExtractSpanContext(ctx), "text/plains")
	return resp.Proto(), nil
}

func (m *mockGRPCAPI) PublishEvent(ctx context.Context, in *daprv1pb.PublishEventEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) InvokeService(ctx context.Context, in *daprv1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	return &commonv1pb.InvokeResponse{}, nil
}

func (m *mockGRPCAPI) InvokeBinding(ctx context.Context, in *daprv1pb.InvokeBindingEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) GetState(ctx context.Context, in *daprv1pb.GetStateEnvelope) (*daprv1pb.GetStateResponseEnvelope, error) {
	return &daprv1pb.GetStateResponseEnvelope{}, nil
}

func (m *mockGRPCAPI) SaveState(ctx context.Context, in *daprv1pb.SaveStateEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) DeleteState(ctx context.Context, in *daprv1pb.DeleteStateEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) GetSecret(ctx context.Context, in *daprv1pb.GetSecretEnvelope) (*daprv1pb.GetSecretResponseEnvelope, error) {
	return &daprv1pb.GetSecretResponseEnvelope{}, nil
}

func ExtractSpanContext(ctx context.Context) []byte {
	sc, _ := ctx.Value(diag.DaprTraceContextKey{}).(trace.SpanContext)
	return []byte(SerializeSpanContext(sc))
}

// SerializeSpanContext serializes a span context into a simple string
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

func configureTestTraceExporter(meta exporters.Metadata) {
	exporter := stringexporter.NewStringExporter(logger.NewLogger("fakeLogger"))
	exporter.Init("fakeID", "fakeAddress", meta)
}

func startTestServerWithTracing(port int) (*grpc_go.Server, *string) {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	var buffer = ""
	configureTestTraceExporter(exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	})

	spec := config.TracingSpec{SamplingRate: "1"}
	server := grpc_go.NewServer(
		grpc_go.StreamInterceptor(grpc_middleware.ChainStreamServer(diag.SetTracingSpanContextGRPCMiddlewareStream(spec))),
		grpc_go.UnaryInterceptor(grpc_middleware.ChainUnaryServer(diag.SetTracingSpanContextGRPCMiddlewareUnary(spec))),
	)

	go func() {
		internalv1pb.RegisterDaprInternalServer(server, &mockGRPCAPI{})
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server, &buffer
}

func startTestServer(port int) *grpc_go.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, &mockGRPCAPI{})
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

func startInternalServer(port int, testAPIServer *api) *grpc_go.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	server := grpc_go.NewServer()
	go func() {
		internalv1pb.RegisterDaprInternalServer(server, testAPIServer)
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

func startDaprAPIServer(port int, testAPIServer *api) *grpc_go.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, testAPIServer)
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

func createTestClient(port int) *grpc_go.ClientConn {
	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	if err != nil {
		panic(err)
	}
	return conn
}

func TestCallActorWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server, _ := startTestServerWithTracing(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := internalv1pb.NewDaprInternalClient(clientConn)

	request := invokev1.NewInvokeMethodRequest("method")
	request.WithActor("test-actor", "actor-1")

	resp, err := client.CallActor(context.Background(), request.Proto())
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.GetMessage(), "failed to generate trace context with actor call")
}

func TestCallRemoteAppWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server, _ := startTestServerWithTracing(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := internalv1pb.NewDaprInternalClient(clientConn)
	request := invokev1.NewInvokeMethodRequest("method").Proto()

	resp, err := client.CallLocal(context.Background(), request)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.GetMessage(), "failed to generate trace context with app call")
}

func TestCallLocal(t *testing.T) {
	t.Run("appchannel is not ready", func(t *testing.T) {
		port, _ := freeport.GetFreePort()

		fakeAPI := &api{
			id:         "fakeAPI",
			appChannel: nil,
		}
		server := startInternalServer(port, fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := internalv1pb.NewDaprInternalClient(clientConn)
		request := invokev1.NewInvokeMethodRequest("method").Proto()

		_, err := client.CallLocal(context.Background(), request)
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("parsing InternalInvokeRequest is failed", func(t *testing.T) {
		port, _ := freeport.GetFreePort()

		mockAppChannel := new(channelt.MockAppChannel)
		fakeAPI := &api{
			id:         "fakeAPI",
			appChannel: mockAppChannel,
		}
		server := startInternalServer(port, fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := internalv1pb.NewDaprInternalClient(clientConn)
		request := &internalv1pb.InternalInvokeRequest{
			Message: &any.Any{Value: []byte("fake")},
		}

		_, err := client.CallLocal(context.Background(), request)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("invokemethod returns error", func(t *testing.T) {
		port, _ := freeport.GetFreePort()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(nil, status.Error(codes.Unknown, "unknown error"))
		fakeAPI := &api{
			id:         "fakeAPI",
			appChannel: mockAppChannel,
		}
		server := startInternalServer(port, fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := internalv1pb.NewDaprInternalClient(clientConn)
		request := invokev1.NewInvokeMethodRequest("method").Proto()

		_, err := client.CallLocal(context.Background(), request)
		assert.Equal(t, codes.Unknown, status.Code(err))
	})
}

func mustMarshalAny(msg proto.Message) *any.Any {
	any, err := ptypes.MarshalAny(msg)
	if err != nil {
		panic(fmt.Sprintf("ptypes.MarshalAny(%+v) failed: %v", msg, err))
	}
	return any
}

func TestInvokeService(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	// Setup Dapr API server
	fakeAPI := &api{
		id:              "fakeAPI",
		directMessaging: mockDirectMessaging,
	}

	t.Run("handle http response code", func(t *testing.T) {
		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil)
		fakeResp.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.AnythingOfType("*context.valueCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, fakeAPI)
		defer server.Stop()

		// Create gRPC test client
		clientConn := createTestClient(port)
		defer clientConn.Close()

		// act
		client := daprv1pb.NewDaprClient(clientConn)
		req := &daprv1pb.InvokeServiceRequest{
			Id: "fakeAppID",
			Message: &commonv1pb.InvokeRequest{
				Method: "fakeMethod",
				Data:   &any.Any{Value: []byte("testData")},
			},
		}
		_, err := client.InvokeService(context.Background(), req)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.NotFound, s.Code())
		assert.Equal(t, "Not Found", s.Message())

		errInfo := s.Details()[0].(*epb.ErrorInfo)
		assert.Equal(t, 1, len(s.Details()))
		assert.Equal(t, "404", errInfo.Metadata["http.code"])
		assert.Equal(t, "fakeDirectMessageResponse", errInfo.Metadata["http.error_message"])
	})

	t.Run("handle grpc response code", func(t *testing.T) {
		fakeResp := invokev1.NewInvokeMethodResponse(
			int32(codes.Unimplemented), "Unimplemented",
			[]*any.Any{
				mustMarshalAny(&epb.ResourceInfo{
					ResourceType: "sidecar",
					ResourceName: "invoke/service",
					Owner:        "Dapr",
				}),
			},
		)
		fakeResp.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.AnythingOfType("*context.valueCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, fakeAPI)
		defer server.Stop()

		// Create gRPC test client
		clientConn := createTestClient(port)
		defer clientConn.Close()

		// act
		client := daprv1pb.NewDaprClient(clientConn)
		req := &daprv1pb.InvokeServiceRequest{
			Id: "fakeAppID",
			Message: &commonv1pb.InvokeRequest{
				Method: "fakeMethod",
				Data:   &any.Any{Value: []byte("testData")},
			},
		}
		_, err := client.InvokeService(context.Background(), req)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unimplemented, s.Code())
		assert.Equal(t, "Unimplemented", s.Message())

		errInfo := s.Details()[0].(*epb.ResourceInfo)
		assert.Equal(t, 1, len(s.Details()))
		assert.Equal(t, "sidecar", errInfo.GetResourceType())
		assert.Equal(t, "invoke/service", errInfo.GetResourceName())
		assert.Equal(t, "Dapr", errInfo.GetOwner())
	})
}

func TestSaveState(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	request := &daprv1pb.SaveStateEnvelope{
		Requests: []*daprv1pb.StateRequest{
			{
				Key:   "1",
				Value: &any.Any{Value: []byte("2")},
			},
		},
	}

	_, err := client.SaveState(context.Background(), request)
	assert.Nil(t, err)
}

func TestGetState(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.GetState(context.Background(), &daprv1pb.GetStateEnvelope{})
	assert.Nil(t, err)
}

func TestDeleteState(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.DeleteState(context.Background(), &daprv1pb.DeleteStateEnvelope{})
	assert.Nil(t, err)
}

func TestPublishTopic(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.PublishEvent(context.Background(), &daprv1pb.PublishEventEnvelope{})
	assert.Nil(t, err)
}

func TestInvokeBinding(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.InvokeBinding(context.Background(), &daprv1pb.InvokeBindingEnvelope{})
	assert.Nil(t, err)
}
