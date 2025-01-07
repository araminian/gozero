package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/araminian/gozero/internal/config"
	grpcclient "github.com/araminian/grpc-simple-app/client"
	pb "github.com/araminian/grpc-simple-app/proto/todo/v2"
	grpcserver "github.com/araminian/grpc-simple-app/server"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type testServer struct {
	server *http.Server
	port   string
}

// testConfig holds common test configuration
type testConfig struct {
	proxyPort  int
	targetPort string
	headers    map[string]string
}

// setupTestConfig returns a default test configuration
func setupTestConfig(targetPort string) testConfig {
	return testConfig{
		proxyPort:  8080,
		targetPort: targetPort,
		headers: map[string]string{
			"X-Gozero-Target-Port":    targetPort,
			"X-Gozero-Target-Host":    "localhost",
			"X-Gozero-Target-Scheme":  "http",
			"X-Gozero-Target-Retries": "10",
			"X-Gozero-Target-Backoff": "100ms",
		},
	}
}

// setupProxy creates and starts a proxy server for testing
func setupProxy(t *testing.T, cfg testConfig) (*HTTPReverseProxy, context.CancelFunc) {
	t.Helper()
	config.InitLogger(zapcore.ErrorLevel)

	proxy, err := NewHTTPReverseProxy(WithListenPort(cfg.proxyPort), WithBufferSize(1024))
	if err != nil {
		t.Fatalf("failed to create http proxy: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go proxy.Start(ctx)

	return proxy, cancel
}

// setupHTTP1Server creates and starts an HTTP/1.1 test server
func setupHTTP1Server(t *testing.T, port string) *testServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/pass", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello, World!"))
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	})

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go server.ListenAndServe()
	return &testServer{server: server, port: port}
}

// setupHTTP2Server creates and starts an HTTP/2 test server
func setupHTTP2Server(t *testing.T, port string) *testServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/pass", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello, World!"))
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	})

	h2s := &http2.Server{}
	h2chandler := h2c.NewHandler(mux, h2s)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: h2chandler,
	}

	if err := http2.ConfigureServer(server, h2s); err != nil {
		t.Fatalf("failed to configure HTTP/2 server: %v", err)
	}

	go server.ListenAndServe()
	return &testServer{server: server, port: port}
}

// createHTTP2Client returns an HTTP client configured for HTTP/2
func createHTTP2Client() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
}

// makeRequest makes an HTTP request with the given configuration
func makeRequest(t *testing.T, client *http.Client, method, path string, cfg testConfig) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method,
		fmt.Sprintf("http://localhost:%d%s", cfg.proxyPort, path), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	for k, v := range cfg.headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}

	return resp
}

func TestHTTPReverseProxy(t *testing.T) {
	cfg := setupTestConfig("8081")
	proxy, cancel := setupProxy(t, cfg)
	defer cancel()
	defer proxy.Shutdown(context.Background())

	server := setupHTTP1Server(t, cfg.targetPort)
	defer server.server.Shutdown(context.Background())

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "successful request",
			path:           "/pass",
			expectedStatus: http.StatusOK,
			expectedBody:   "Hello, World!",
		},
		{
			name:           "failed request",
			path:           "/fail",
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeRequest(t, http.DefaultClient, "GET", tt.path, cfg)
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("expected status code %d, got %d", tt.expectedStatus, resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}

			if string(body) != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, string(body))
			}
		})
	}
}

func TestHTTPReverseProxyHTTP2(t *testing.T) {
	cfg := setupTestConfig("8081")
	proxy, cancel := setupProxy(t, cfg)
	defer cancel()
	defer proxy.Shutdown(context.Background())

	server := setupHTTP2Server(t, cfg.targetPort)
	defer server.server.Shutdown(context.Background())

	client := createHTTP2Client()

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "successful request",
			path:           "/pass",
			expectedStatus: http.StatusOK,
			expectedBody:   "Hello, World!",
		},
		{
			name:           "failed request",
			path:           "/fail",
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeRequest(t, client, "GET", tt.path, cfg)
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("expected status code %d, got %d", tt.expectedStatus, resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}

			if string(body) != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, string(body))
			}
		})
	}
}

func TestHTTPReverseProxyGRPC(t *testing.T) {
	cfg := setupTestConfig("8081")
	proxy, cancel := setupProxy(t, cfg)
	defer cancel()
	defer proxy.Shutdown(context.Background())

	// Setup gRPC server
	listener, err := net.Listen("tcp", "localhost:"+cfg.targetPort)
	if err != nil {
		t.Fatalf("failed to listen for grpc server: %v", err)
	}

	server, err := grpcserver.NewServer()
	if err != nil {
		t.Fatalf("failed to create grpc server: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := server.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			t.Errorf("failed to serve grpc server: %v", err)
		}
	}()

	defer func() {
		server.GracefulStop()
		listener.Close()
		wg.Wait()
	}()

	// Setup gRPC client
	client, conn, err := grpcclient.NewClient(fmt.Sprintf("localhost:%d", cfg.proxyPort))
	if err != nil {
		t.Fatalf("failed to create grpc client: %v", err)
	}
	defer conn.Close()

	mask, err := grpcclient.NewMask()
	if err != nil {
		t.Fatalf("failed to create grpc mask: %v", err)
	}

	// Test cases
	t.Run("CRUD operations", func(t *testing.T) {
		// Add tasks
		dueDate := time.Now().Add(time.Hour * 24)
		pastDueDate := time.Now().Add(time.Second * 5)

		grpcclient.AddTask(client, "Buy milk", dueDate, cfg.headers)
		grpcclient.AddTask(client, "Buy milk overdue", pastDueDate, cfg.headers)
		time.Sleep(time.Second * 5)

		// List tasks
		grpcclient.PrintTasks(client, mask, cfg.headers)

		// Update tasks
		id1 := grpcclient.AddTask(client, "Buy bread", dueDate, cfg.headers)
		id2 := grpcclient.AddTask(client, "Buy eggs", dueDate, cfg.headers)

		updates := []*pb.UpdateTaskRequest{
			{Id: id1, Done: false, Description: "Buy 2 bread", DueDate: timestamppb.New(dueDate)},
			{Id: id2, Done: true, Description: "Buy 3 eggs", DueDate: timestamppb.New(dueDate)},
		}
		grpcclient.UpdateTask(client, updates, cfg.headers)
		grpcclient.PrintTasks(client, mask, cfg.headers)

		// Delete tasks
		deletes := []*pb.DeleteTaskRequest{
			{Id: id1},
			{Id: id2},
		}
		grpcclient.DeleteTask(client, cfg.headers, deletes...)
		grpcclient.PrintTasks(client, mask, cfg.headers)
	})
}
