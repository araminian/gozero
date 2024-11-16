package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"

	"github.com/araminian/gozero/internal/config"
)

type HTTPReverseProxyConfig func(*httpReverseProxyConfig) error

const (
	defaultTimeout               = 10 * time.Minute
	defaultPort                  = 8443
	defaultBuffer                = 1000
	defaultCacheDuration         = 3 * time.Minute
	defaultCacheCleanup          = 10 * time.Minute
	targetHostHeader             = "X-Gozero-Target-Host"
	targetPortHeader             = "X-Gozero-Target-Port"
	targetSchemeHeader           = "X-Gozero-Target-Scheme"
	targetHealthPathHeader       = "X-Gozero-Target-Health-Path"
	targetHealthRetriesHeader    = "X-Gozero-Target-Health-Retries"
	defaultTargetHealthPath      = "/"
	defaultTargetPort            = 443
	defaultTargetScheme          = "https"
	defaultMaxRetries            = 20
	defaultInitialBackoff        = 100 * time.Millisecond
	defaultMaxBackoff            = 2 * time.Second
	defaultIdleTimeout           = 120 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
)

type httpReverseProxyConfig struct {
	timeout       *time.Duration
	listenPort    *int
	requestBuffer *int
}

type HTTPReverseProxy struct {
	timeout    time.Duration
	listenPort int
	httpServer *http.Server

	requestBufferSize int
	requestsCh        chan Requests

	cache *cache.Cache
}

func WithBufferSize(buffer int) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.requestBuffer = &buffer
		return nil
	}
}

func WithTimeout(timeout time.Duration) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.timeout = &timeout
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
		timeout           time.Duration = defaultTimeout
		listenPort        int           = defaultPort
		requestBufferSize int           = defaultBuffer
	)
	if cfg.timeout != nil {
		timeout = *cfg.timeout
	}

	if cfg.listenPort != nil {
		listenPort = *cfg.listenPort
	}

	if cfg.requestBuffer != nil {
		requestBufferSize = *cfg.requestBuffer
	}

	return &HTTPReverseProxy{
		timeout:           timeout,
		listenPort:        listenPort,
		requestBufferSize: requestBufferSize,
		requestsCh:        make(chan Requests, requestBufferSize),
		cache:             cache.New(defaultCacheDuration, defaultCacheCleanup),
	}, nil
}

func (p *HTTPReverseProxy) Shutdown(ctx context.Context) error {
	close(p.requestsCh)
	return p.httpServer.Shutdown(ctx)
}

func (p *HTTPReverseProxy) Start(ctx context.Context) error {
	proxy := &httputil.ReverseProxy{
		Director: p.httpDirector,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.URL.Scheme == "error" {
				http.Error(w, "Service unavailable or starting up", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
	}

	// TODO: Think about Connection Pooling ->  MaxIdleConns , MaxIdleConnsPerHost
	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		IdleConnTimeout:       defaultIdleTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}

	server := &http.Server{
		Addr:      fmt.Sprintf(":%d", p.listenPort),
		TLSConfig: tlsConfig,
		Handler:   proxy,
	}

	p.httpServer = server

	go func() {
		config.Log.Infof("Starting reverse proxy server on port %d", p.listenPort)
		//err := server.ListenAndServeTLS("server.crt", "server.key")
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			config.Log.Errorf("Error starting reverse proxy server: %+v", err)
			return
		}
	}()

	<-ctx.Done()

	config.Log.Debugf("Reverse proxy server on port %d shutting down...", p.listenPort)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		config.Log.Errorf("Failed to shutdown reverse proxy server: %+v", err)
	}

	return nil
}

func (p *HTTPReverseProxy) httpDirector(req *http.Request) {

	var targetHost string

	isDev := config.GetEnvOrDefaultString("IS_DEV", "false") == "true"
	if isDev {
		targetHost = "www.trivago.com"
	} else {
		targetHost = req.Header.Get(targetHostHeader)
		if targetHost == "" {
			config.Log.Errorf("Target host is not set using header %s", targetHostHeader)
			return
		}
	}

	config.Log.Debugf("Proxying request '%s' to '%s'", req.URL.String(), targetHost)

	originalScheme := req.Header.Get(targetSchemeHeader)
	if originalScheme == "" {
		originalScheme = defaultTargetScheme
		config.Log.Debugf("Target scheme is not set using default value '%s'", originalScheme)
	}

	targetPort := req.Header.Get(targetPortHeader)
	if targetPort == "" {
		targetPort = fmt.Sprintf("%d", defaultTargetPort)
		config.Log.Debugf("Target port is not set using default value '%s'", targetPort)
	}

	targetURL, err := url.Parse(fmt.Sprintf("%s://%s:%s", originalScheme, targetHost, targetPort))
	if err != nil {
		config.Log.Errorf("Error parsing target URL: %v", err)
		return
	}

	path, _ := joinURLPath(targetURL, req.URL)
	p.requestsCh <- Requests{
		Host: targetURL.Host,
		Path: path,
	}
	config.Log.Debugf("Sending request to '%s'", path)

	healthPath := req.Header.Get(targetHealthPathHeader)
	if healthPath == "" {
		healthPath = defaultTargetHealthPath
		config.Log.Debugf("Target health path is not set using default value '%s'", healthPath)
	}
	retries, err := strconv.Atoi(req.Header.Get(targetHealthRetriesHeader))
	if err != nil {
		retries = defaultMaxRetries
		config.Log.Debugf("Target health retries is not set using default value '%d'", retries)
	}

	if _, ok := p.cache.Get(targetURL.Host); !ok {
		config.Log.Debugf("service '%s' is not in health check cache", targetURL.Host)
		config.Log.Debugf("Checking service availability for '%s'", targetURL.Host)
		if err := p.checkServiceAvailability(targetURL.Host, healthPath, originalScheme, retries); err != nil {
			config.Log.Errorf("Service unavailability detected: %v", err)
			req.URL.Scheme = "error"
			return
		}
		config.Log.Debugf("Service '%s' is available, adding to cache", targetURL.Host)
		p.cache.Set(targetURL.Host, struct{}{}, defaultCacheDuration)
	}

	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.URL.Path, req.URL.RawPath = joinURLPath(targetURL, req.URL)
	req.Host = targetURL.Host

	if _, ok := req.Header["X-Forwarded-For"]; !ok {
		req.Header.Set("X-Forwarded-For", req.RemoteAddr)
	}
	req.Header.Set("X-Forwarded-Host", req.Host)
	req.Header.Set("X-Forwarded-Proto", req.URL.Scheme)

	config.Log.Debugf("Proxying request to: %s://%s%s", req.URL.Scheme, req.URL.Host, req.URL.Path)

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

func (p *HTTPReverseProxy) checkServiceAvailability(host string, path string, scheme string, retries int) error {
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		hostname = host
		port = fmt.Sprintf("%d", defaultTargetPort)
	}

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		config.Log.Debugf("Checking service availability for '%s' (attempt %d)", host, attempt)
		healthURL := fmt.Sprintf("%s://%s:%s%s", scheme, hostname, port, path)
		resp, err := client.Get(healthURL)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			config.Log.Debugf("Health check failed '%s' with status code: %d", healthURL, resp.StatusCode)
			lastErr = fmt.Errorf("health check failed with status code: %d", resp.StatusCode)
		} else {
			config.Log.Debugf("Health check failed '%s' with error: %v", healthURL, err)
			lastErr = err
		}

		backoff := defaultInitialBackoff * time.Duration(1<<attempt)
		if backoff > defaultMaxBackoff {
			backoff = defaultMaxBackoff
		}
		config.Log.Debugf("Backing off for host '%s' for %s", host, backoff)
		time.Sleep(backoff)
	}
	return fmt.Errorf("service unavailable after %d attempts: %v", retries, lastErr)
}
