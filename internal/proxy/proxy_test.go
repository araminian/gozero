package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/araminian/gozero/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestHTTPReverseProxy(t *testing.T) {

	config.InitLogger(zapcore.ErrorLevel)
	proxy, err := NewHTTPReverseProxy(WithListenPort(8080), WithBufferSize(1024))
	if err != nil {
		t.Fatalf("failed to create http proxy: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx)
	defer proxy.Shutdown(ctx)

	// Start a backend server to listen on port 8081
	backend := http.NewServeMux()
	backend.HandleFunc("/pass", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello, World!"))
	})

	backend.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	})

	backendServer := &http.Server{
		Addr:    ":8081",
		Handler: backend,
	}

	go backendServer.ListenAndServe()
	defer backendServer.Shutdown(context.Background())

	// Make a request to the proxy
	passRequest, err := http.NewRequest("GET", "http://localhost:8080/pass", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Set Gozero headers
	passRequest.Header.Set("X-Gozero-Target-Port", "8081")
	passRequest.Header.Set("X-Gozero-Target-Host", "localhost")
	passRequest.Header.Set("X-Gozero-Target-Scheme", "http")
	passRequest.Header.Set("X-Gozero-Target-Retries", "10")
	passRequest.Header.Set("X-Gozero-Target-Backoff", "100ms")

	passResponse, err := http.DefaultClient.Do(passRequest)
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}

	t.Logf("passResponse: %+v", passResponse)

	body, err := io.ReadAll(passResponse.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	defer passResponse.Body.Close()

	if string(body) != "Hello, World!" {
		t.Fatalf("expected response body to be 'Hello, World!', got %s", string(body))
	}

	if passResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status code to be %d, got %d", http.StatusOK, passResponse.StatusCode)
	}

	failRequest, err := http.NewRequest("GET", "http://localhost:8080/fail", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	failRequest.Header.Set("X-Gozero-Target-Port", "8081")
	failRequest.Header.Set("X-Gozero-Target-Host", "localhost")
	failRequest.Header.Set("X-Gozero-Target-Scheme", "http")
	failRequest.Header.Set("X-Gozero-Target-Retries", "10")
	failRequest.Header.Set("X-Gozero-Target-Backoff", "100ms")

	failResponse, err := http.DefaultClient.Do(failRequest)
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}

	t.Logf("failResponse: %+v", failResponse)

	defer failResponse.Body.Close()

	if failResponse.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status code to be %d, got %d", http.StatusInternalServerError, failResponse.StatusCode)
	}
}

func TestHTTPReverseProxyHTTP2(t *testing.T) {

	config.InitLogger(zapcore.ErrorLevel)
	proxy, err := NewHTTPReverseProxy(WithListenPort(8080), WithBufferSize(1024))
	if err != nil {
		t.Fatalf("failed to create http proxy: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx)
	defer proxy.Shutdown(ctx)

	// Create a HTTP2 server
	backendHandler := http.NewServeMux()
	backendHandler.HandleFunc("/pass", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello, World!"))
	})

	backendHandler.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	})

	h2s := &http2.Server{}
	h2chandler := h2c.NewHandler(backendHandler, h2s)

	backendServer := &http.Server{
		Addr:    ":8081",
		Handler: h2chandler,
	}

	err = http2.ConfigureServer(backendServer, h2s)
	if err != nil {
		config.Log.Error("Error configuring HTTP/2 server", zap.Error(err))
		panic(err)
	}

	go backendServer.ListenAndServe()
	defer backendServer.Shutdown(context.Background())

	// Create a custom HTTP/2 client
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true, // Enable HTTP/2 over cleartext TCP
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr) // Force non-TLS
			},
		},
	}

	// Make a HTTP2 request to the proxy
	passRequest, err := http.NewRequest("GET", "http://localhost:8080/pass", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Set Gozero headers
	passRequest.Header.Set("X-Gozero-Target-Port", "8081")
	passRequest.Header.Set("X-Gozero-Target-Host", "localhost")
	passRequest.Header.Set("X-Gozero-Target-Scheme", "http")
	passRequest.Header.Set("X-Gozero-Target-Retries", "10")
	passRequest.Header.Set("X-Gozero-Target-Backoff", "100ms")

	passResponse, err := client.Do(passRequest)
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}

	t.Logf("passResponse: %+v", passResponse)

	defer passResponse.Body.Close()

	if passResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status code to be %d, got %d", http.StatusOK, passResponse.StatusCode)
	}

	body, err := io.ReadAll(passResponse.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	if string(body) != "Hello, World!" {
		t.Fatalf("expected response body to be 'Hello, World!', got %s", string(body))
	}

	failRequest, err := http.NewRequest("GET", "http://localhost:8080/fail", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	failRequest.Header.Set("X-Gozero-Target-Port", "8081")
	failRequest.Header.Set("X-Gozero-Target-Host", "localhost")
	failRequest.Header.Set("X-Gozero-Target-Scheme", "http")
	failRequest.Header.Set("X-Gozero-Target-Retries", "10")
	failRequest.Header.Set("X-Gozero-Target-Backoff", "100ms")

	failResponse, err := client.Do(failRequest)
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}

	t.Logf("failResponse: %+v", failResponse)

	defer failResponse.Body.Close()

	if failResponse.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status code to be %d, got %d", http.StatusInternalServerError, failResponse.StatusCode)
	}
}

func TestHTTPReverseProxyGRPC(t *testing.T) {

	t.Skip("skipping grpc test, need to implement")
	config.InitLogger(zapcore.ErrorLevel)
	proxy, err := NewHTTPReverseProxy(WithListenPort(8080), WithBufferSize(1024))
	if err != nil {
		t.Fatalf("failed to create http proxy: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx)
	defer proxy.Shutdown(ctx)

}
