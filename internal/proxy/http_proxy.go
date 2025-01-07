package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/eapache/go-resiliency/retrier"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/araminian/gozero/internal/config"
)

type HTTPReverseProxyConfig func(*httpReverseProxyConfig) error

const (
	defaultTimeout               = 10 * time.Minute
	defaultPort                  = 8443
	defaultBuffer                = 1000
	targetHostHeader             = "X-Gozero-Target-Host"
	targetPortHeader             = "X-Gozero-Target-Port"
	targetSchemeHeader           = "X-Gozero-Target-Scheme"
	targetRetriesHeader          = "X-Gozero-Target-Retries"
	targetBackoffHeader          = "X-Gozero-Target-Backoff"
	defaultTargetPort            = 443
	defaultTargetScheme          = "https"
	defaultMaxRetries            = 20
	defaultInitialBackoff        = 100 * time.Millisecond
	defaultIdleTimeout           = 120 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	defaultDialTimeout           = 300 * time.Second
)

type httpReverseProxyConfig struct {
	listenPort    *int
	requestBuffer *int
}

type HTTPReverseProxy struct {
	listenPort int
	httpServer *http.Server

	requestBufferSize int
	requestsCh        chan Requests
}

func WithBufferSize(buffer int) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.requestBuffer = &buffer
		return nil
	}
}

func WithListenPort(port int) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.listenPort = &port
		return nil
	}
}

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

type retryRoundTripper struct {
	next http.RoundTripper
}

func (rr *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {

	maxRetries, err := strconv.Atoi(req.Header.Get(targetRetriesHeader))
	if err != nil {
		maxRetries = defaultMaxRetries
	}

	backoff, err := time.ParseDuration(req.Header.Get(targetBackoffHeader))
	if err != nil {
		backoff = defaultInitialBackoff
	}

	re := retrier.New(retrier.ExponentialBackoff(maxRetries, backoff), nil)

	var resp *http.Response
	var respErr error

	targetHost := req.Host
	originalHost := req.Header.Get("X-Forwarded-Host")

	respErr = re.Run(func() error {
		config.Log.Debug("Sending request", zap.String("from", originalHost), zap.String("to", targetHost))
		resp, respErr = rr.next.RoundTrip(req)
		if respErr != nil {
			config.Log.Debug("Request failed, will retry", zap.Error(respErr), zap.String("from", originalHost), zap.String("to", targetHost))
			return respErr
		}
		// TODO: Should i check for 404?

		noHealthyUpstreamValue := "no healthy upstream"
		noHealthyUpstreamStatusCode := http.StatusServiceUnavailable

		if resp.StatusCode == noHealthyUpstreamStatusCode {
			bodyBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			if err == nil && strings.Contains(string(bodyBytes), noHealthyUpstreamValue) {
				msg := fmt.Sprintf("service '%s' -> '%s' is not available: status code: %d", originalHost, targetHost, resp.StatusCode)
				config.Log.Debug("service is not available", zap.String("Status", resp.Status), zap.String("from", originalHost), zap.String("to", targetHost))
				return errors.New(msg)
			}
		}

		return nil
	})

	if respErr != nil {
		msg := fmt.Sprintf("all retry attempts failed for service '%s' -> '%s': %v. Service failed to scaled up or not passing probes", originalHost, targetHost, respErr)
		config.Log.Error("all retry attempts failed", zap.String("from", originalHost), zap.String("To", targetHost), zap.Error(respErr))
		return resp, errors.New(msg)
	}

	return resp, nil
}

func (p *HTTPReverseProxy) Shutdown(ctx context.Context) error {
	close(p.requestsCh)
	return p.httpServer.Shutdown(ctx)
}

func (p *HTTPReverseProxy) handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	if r.URL.Scheme == "error" {
		http.Error(w, "Service unavailable or starting up", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

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
	responseData, err := httputil.DumpResponse(r, true)
	if err != nil {
		return err
	}
	config.Log.Debug("Proxy response", zap.String("status", r.Status), zap.String("host", r.Request.Host), zap.String("path", r.Request.URL.Path), zap.String("method", r.Request.Method), zap.Int64("contentLength", r.ContentLength), zap.Any("requestHeaders", r.Request.Header), zap.Any("responseHeaders", r.Header), zap.String("body", string(responseData)))

	r.Header.Del("Content-Security-Policy")
	r.Header.Del("Referrer-Policy")
	return nil
}

func (p *HTTPReverseProxy) Start(ctx context.Context) error {

	http2Transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		},
	}

	http1Transport := &http.Transport{
		IdleConnTimeout:       defaultIdleTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
		DialContext: (&net.Dialer{
			Timeout: defaultDialTimeout,
		}).DialContext,
	}

	proxy := &httputil.ReverseProxy{
		Director:       p.httpDirector,
		ErrorHandler:   p.handleProxyError,
		ModifyResponse: p.modifyProxyResponse,
		Transport: &retryRoundTripper{
			next: &conditionalTransport{
				h2Transport: http2Transport,
				h1Transport: http1Transport,
			},
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
		panic(err)
	}

	p.httpServer = server

	go func() {
		config.Log.Info("Starting reverse proxy server", zap.Int("port", p.listenPort))
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			config.Log.Error("Error starting reverse proxy server", zap.Error(err))
			return
		}
	}()

	<-ctx.Done()

	config.Log.Info("Reverse proxy server shutting down", zap.Int("port", p.listenPort))
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		config.Log.Error("Failed to shutdown reverse proxy server", zap.Error(err))
	}

	return nil
}

type conditionalTransport struct {
	h2Transport *http2.Transport
	h1Transport *http.Transport
}

func (t *conditionalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Proto == "HTTP/2.0" {
		config.Log.Debug("Protocol: HTTP/2.0, Using HTTP/2 transport for request", zap.String("url", req.URL.String()))
		return t.h2Transport.RoundTrip(req)
	}
	config.Log.Debug("Protocol: HTTP/1.1, Using HTTP/1.1 transport for request", zap.String("url", req.URL.String()))
	return t.h1Transport.RoundTrip(req)
}

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

func joinURLPath(a, b *url.URL) (path, rawpath string) {
	apath := a.EscapedPath()
	if apath == "" {
		apath = "/"
	}
	bpath := b.EscapedPath()
	if bpath == "" {
		bpath = "/"
	}
	if strings.HasSuffix(apath, "/") && strings.HasPrefix(bpath, "/") {
		apath = apath + bpath[1:]
	} else {
		apath = apath + bpath
	}
	unescaped, err := url.PathUnescape(apath)
	if err != nil {
		return apath, apath
	}
	return unescaped, apath
}

func (p *HTTPReverseProxy) Requests() <-chan Requests {
	return p.requestsCh
}
