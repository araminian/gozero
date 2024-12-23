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
	"github.com/sirupsen/logrus"
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
		config.Log.Debugf("Retrying request to '%s' -> '%s'", originalHost, targetHost)
		resp, respErr = rr.next.RoundTrip(req)
		if respErr != nil {
			config.Log.Debugf("Request failed, will retry: %v", respErr)
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
				config.Log.Debugf(msg)
				return errors.New(msg)
			}
		}

		return nil
	})

	if respErr != nil {
		msg := fmt.Sprintf("all retry attempts failed for service '%s' -> '%s': %v. Service failed to scaled up or not passing probes", originalHost, targetHost, respErr)
		config.Log.Errorf(msg)
		return resp, errors.New(msg)
	}

	return resp, nil
}

func (p *HTTPReverseProxy) Shutdown(ctx context.Context) error {
	close(p.requestsCh)
	return p.httpServer.Shutdown(ctx)
}

func handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	if r.URL.Scheme == "error" {
		http.Error(w, "Service unavailable or starting up", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func modifyProxyResponse(r *http.Response) error {
	if loc := r.Header.Get("Location"); loc != "" {
		config.Log.Debugf("Original Location header: %s", loc)
		if u, err := url.Parse(loc); err == nil {
			originalHost := u.Host
			u.Host = r.Request.Header.Get("X-Forwarded-Host")
			u.Scheme = r.Request.Header.Get("X-Forwarded-Proto")
			newLocation := u.String()
			r.Header.Set("Location", newLocation)
			config.Log.Debugf("Updated Location header from %s to %s", originalHost, r.Request.Host)
		} else {
			config.Log.Warnf("Failed to parse Location header %s: %v", loc, err)
		}
	}
	responseData, err := httputil.DumpResponse(r, true)
	if err != nil {
		return err
	}
	config.Log.WithFields(logrus.Fields{
		"status":          r.Status,
		"host":            r.Request.Host,
		"path":            r.Request.URL.Path,
		"method":          r.Request.Method,
		"contentLength":   r.ContentLength,
		"requestHeaders":  r.Request.Header,
		"responseHeaders": r.Header,
		"body":            string(responseData),
	}).Debug("Proxy response")

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
		ErrorHandler:   handleProxyError,
		ModifyResponse: modifyProxyResponse,
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
		config.Log.Errorf("Error configuring HTTP/2 server: %+v", err)
		panic(err)
	}

	p.httpServer = server

	go func() {
		config.Log.Infof("Starting reverse proxy server on port %d", p.listenPort)
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

type conditionalTransport struct {
	h2Transport *http2.Transport
	h1Transport *http.Transport
}

func (t *conditionalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Proto == "HTTP/2.0" {
		config.Log.Debugf("Protocol: %s, Using HTTP/2 transport for request %s", req.Proto, req.URL.String())
		return t.h2Transport.RoundTrip(req)
	}
	config.Log.Debugf("Protocol: %s, Using HTTP/1.1 transport for request %s", req.Proto, req.URL.String())
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
			config.Log.Errorf("Target host is not set using header %s", targetHostHeader)
			return
		}
	}

	config.Log.Debugf("Proxying request '%s' to '%s'", req.URL.String(), targetHost)

	scheme := req.Header.Get(targetSchemeHeader)
	if scheme == "" {
		scheme = defaultTargetScheme
		config.Log.Debugf("Target scheme is not set using default value '%s'", scheme)
	}

	targetPort := req.Header.Get(targetPortHeader)
	if targetPort == "" {
		targetPort = fmt.Sprintf("%d", defaultTargetPort)
		config.Log.Debugf("Target port is not set using default value '%s'", targetPort)
	}

	targetURL, err := url.Parse(fmt.Sprintf("%s://%s:%s", scheme, targetHost, targetPort))
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

	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.URL.Path, req.URL.RawPath = joinURLPath(targetURL, req.URL)
	req.Host = targetURL.Host

	if _, ok := req.Header["X-Forwarded-For"]; !ok {
		req.Header.Set("X-Forwarded-For", req.RemoteAddr)
	}
	req.Header.Set("X-Forwarded-Host", originalHost)
	req.Header.Set("X-Forwarded-Proto", originalScheme)

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
