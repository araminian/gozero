package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/araminian/gozero/internal/config"
)

// NewHTTPReverseProxy creates a new HTTP reverse proxy with the given configuration
func NewHTTPReverseProxy(configs ...HTTPReverseProxyConfig) (*HTTPReverseProxy, error) {
	cfg := &httpReverseProxyConfig{}
	for _, config := range configs {
		if err := config(cfg); err != nil {
			return nil, err
		}
	}
	var (
		listenPort        int = defaultPort
		requestBufferSize int = defaultBuffer
	)

	if cfg.listenPort != nil {
		listenPort = *cfg.listenPort
	}

	if cfg.requestBuffer != nil {
		requestBufferSize = *cfg.requestBuffer
	}

	return &HTTPReverseProxy{
		listenPort:        listenPort,
		requestBufferSize: requestBufferSize,
		requestsCh:        make(chan Requests, requestBufferSize),
	}, nil
}

// Shutdown gracefully shuts down the proxy server
func (p *HTTPReverseProxy) Shutdown(ctx context.Context) error {
	close(p.requestsCh)
	return p.httpServer.Shutdown(ctx)
}

// handleProxyError handles errors that occur during proxying
func (p *HTTPReverseProxy) handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	if r.URL.Scheme == "error" {
		http.Error(w, "Service unavailable or starting up", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

// modifyProxyResponse modifies the response before sending it back to the client
func (p *HTTPReverseProxy) modifyProxyResponse(r *http.Response) error {
	if loc := r.Header.Get("Location"); loc != "" {
		if u, err := url.Parse(loc); err == nil {
			originalHost := u.Host
			u.Host = r.Request.Header.Get("X-Forwarded-Host")
			u.Scheme = r.Request.Header.Get("X-Forwarded-Proto")
			newLocation := u.String()
			r.Header.Set("Location", newLocation)
			config.Log.Debug("Updated Location header", zap.String("from", originalHost), zap.String("to", r.Request.Host))
		} else {
			config.Log.Warn("Failed to parse Location header", zap.String("Location", loc), zap.Error(err))
		}
	}

	if config.Log.Level() == zapcore.DebugLevel {
		responseData, err := httputil.DumpResponse(r, true)
		if err != nil {
			return err
		}
		config.Log.Debug("Proxy response",
			zap.String("status", r.Status),
			zap.String("host", r.Request.Host),
			zap.String("path", r.Request.URL.Path),
			zap.String("method", r.Request.Method),
			zap.Int64("contentLength", r.ContentLength),
			zap.Any("requestHeaders", r.Request.Header),
			zap.Any("responseHeaders", r.Header),
			zap.String("body", string(responseData)))
	}
	r.Header.Del("Content-Security-Policy")
	r.Header.Del("Referrer-Policy")
	return nil
}

// Start starts the proxy server
func (p *HTTPReverseProxy) Start(ctx context.Context) error {
	transport := newConditionalTransport()

	proxy := &httputil.ReverseProxy{
		Director:       p.httpDirector,
		ErrorHandler:   p.handleProxyError,
		ModifyResponse: p.modifyProxyResponse,
		Transport: &retryRoundTripper{
			next: transport,
		},
	}

	h2s := &http2.Server{}
	handler := h2c.NewHandler(proxy, h2s)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", p.listenPort),
		Handler: handler,
	}

	err := http2.ConfigureServer(server, h2s)
	if err != nil {
		config.Log.Error("Error configuring HTTP/2 server", zap.Error(err))
		return err
	}

	p.httpServer = server

	go func() {
		config.Log.Info("Starting reverse proxy server", zap.Int("port", p.listenPort))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			config.Log.Error("Error starting reverse proxy server", zap.Error(err))
			return
		}
	}()

	<-ctx.Done()

	config.Log.Info("Reverse proxy server shutting down", zap.Int("port", p.listenPort))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		config.Log.Error("Failed to shutdown reverse proxy server", zap.Error(err))
		return err
	}

	return nil
}

// httpDirector modifies the request before sending it to the target server
func (p *HTTPReverseProxy) httpDirector(req *http.Request) {
	var targetHost string

	originalHost := req.Host
	originalScheme := req.URL.Scheme
	if originalScheme == "" {
		originalScheme = defaultTargetScheme
	}

	isDev := config.GetEnvOrDefaultString("IS_DEV", "false") == "true"
	if isDev {
		targetHost = "www.trivago.com"
	} else {
		targetHost = req.Header.Get(targetHostHeader)
		if targetHost == "" {
			config.Log.Error("Target host is not set", zap.String("header", targetHostHeader))
			return
		}
	}

	config.Log.Debug("Proxying request", zap.String("from", req.URL.String()), zap.String("to", targetHost))

	scheme := req.Header.Get(targetSchemeHeader)
	if scheme == "" {
		scheme = defaultTargetScheme
		config.Log.Debug("Target scheme is not set", zap.String("scheme", scheme), zap.String("from", req.URL.String()), zap.String("to", targetHost))
	}

	targetPort := req.Header.Get(targetPortHeader)
	if targetPort == "" {
		targetPort = fmt.Sprintf("%d", defaultTargetPort)
		config.Log.Debug("Target port is not set", zap.String("port", targetPort), zap.String("from", req.URL.String()), zap.String("to", targetHost))
	}

	targetURL, err := url.Parse(fmt.Sprintf("%s://%s:%s", scheme, targetHost, targetPort))
	if err != nil {
		config.Log.Error("Error parsing target URL", zap.Error(err), zap.String("from", req.URL.String()), zap.String("to", targetHost))
		return
	}

	path, _ := joinURLPath(targetURL, req.URL)
	p.requestsCh <- Requests{
		Host: targetURL.Host,
		Path: path,
	}
	config.Log.Debug("Sending request", zap.String("path", path), zap.String("from", req.URL.String()), zap.String("to", targetHost))

	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.URL.Path, req.URL.RawPath = joinURLPath(targetURL, req.URL)
	req.Host = targetURL.Host

	if _, ok := req.Header["X-Forwarded-For"]; !ok {
		req.Header.Set("X-Forwarded-For", req.RemoteAddr)
	}
	req.Header.Set("X-Forwarded-Host", originalHost)
	req.Header.Set("X-Forwarded-Proto", originalScheme)

	config.Log.Debug("Proxying request", zap.String("scheme", req.URL.Scheme), zap.String("url", req.URL.String()), zap.String("to", targetHost))
}

// Requests returns a channel of proxy requests
func (p *HTTPReverseProxy) Requests() <-chan Requests {
	return p.requestsCh
}
