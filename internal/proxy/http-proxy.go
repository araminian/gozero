package proxy

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/araminian/gozero/internal/config"
)

type HTTPReverseProxyConfig func(*httpReverseProxyConfig) error

const (
	defaultTimeout            = 10 * time.Minute
	defaultPort               = 8443
	defaultBuffer             = 1000
	targetHostHeader          = "X-Gozero-Target-Host"
	targetPortHeader          = "X-Gozero-Target-Port"
	targetSchemeHeader        = "X-Gozero-Target-Scheme"
	targetHealthPathHeader    = "X-Gozero-Target-Health-Path"
	targetHealthRetriesHeader = "X-Gozero-Target-Health-Retries"
	defaultTargetHealthPath   = "/"
	defaultTargetPort         = 443
	defaultTargetScheme       = "https"
	defaultMaxRetries         = 20
	defaultInitialBackoff     = 100 * time.Millisecond
	defaultMaxBackoff         = 2 * time.Second
)

type httpReverseProxyConfig struct {
	Timeout       *time.Duration
	ListenPort    *int
	RequestBuffer *int
}

type HTTPReverseProxy struct {
	Timeout    time.Duration
	ListenPort int

	RequestBufferSize int
	RequestsCh        chan Requests
}

func WithBufferSize(buffer int) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.RequestBuffer = &buffer
		return nil
	}
}

func WithTimeout(timeout time.Duration) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.Timeout = &timeout
		return nil
	}
}

func WithListenPort(port int) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.ListenPort = &port
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
	if cfg.Timeout != nil {
		timeout = *cfg.Timeout
	}

	if cfg.ListenPort != nil {
		listenPort = *cfg.ListenPort
	}

	if cfg.RequestBuffer != nil {
		requestBufferSize = *cfg.RequestBuffer
	}

	return &HTTPReverseProxy{
		Timeout:           timeout,
		ListenPort:        listenPort,
		RequestBufferSize: requestBufferSize,
		RequestsCh:        make(chan Requests, requestBufferSize),
	}, nil
}

func (p *HTTPReverseProxy) Start() error {
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

	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		IdleConnTimeout:       p.Timeout,
		ResponseHeaderTimeout: p.Timeout,
		TLSHandshakeTimeout:   p.Timeout,
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", p.ListenPort),
		TLSConfig:    tlsConfig,
		Handler:      proxy,
		ReadTimeout:  p.Timeout,
		WriteTimeout: p.Timeout,
		IdleTimeout:  p.Timeout,
	}

	log.Printf("Starting reverse proxy server on port %d", p.ListenPort)
	//err := server.ListenAndServeTLS("server.crt", "server.key")
	err := server.ListenAndServe()
	if err != nil {
		return err
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
		log.Printf("Target host: %s", targetHost)
		if targetHost == "" {
			log.Printf("Target host is not set")
		}
	}

	originalScheme := req.Header.Get(targetSchemeHeader)
	if originalScheme == "" {
		originalScheme = defaultTargetScheme
	}

	targetPort := req.Header.Get(targetPortHeader)
	if targetPort == "" {
		targetPort = fmt.Sprintf("%d", defaultTargetPort)
	}

	targetURL, err := url.Parse(fmt.Sprintf("%s://%s:%s", originalScheme, targetHost, targetPort))
	if err != nil {
		log.Printf("Error parsing target URL: %v", err)
		return
	}

	path, _ := joinURLPath(targetURL, req.URL)
	p.RequestsCh <- Requests{
		Host: targetURL.Host,
		Path: path,
	}

	healthPath := req.Header.Get(targetHealthPathHeader)
	if healthPath == "" {
		healthPath = defaultTargetHealthPath
	}
	retries, err := strconv.Atoi(req.Header.Get(targetHealthRetriesHeader))
	if err != nil {
		retries = defaultMaxRetries
	}

	if err := p.checkServiceAvailability(targetURL.Host, healthPath, originalScheme, retries); err != nil {
		log.Printf("Service unavailability detected: %v", err)
		req.URL.Scheme = "error"
		return
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

	log.Printf("Proxying request to: %s://%s%s", req.URL.Scheme, req.URL.Host, req.URL.Path)

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
	return p.RequestsCh
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
		healthURL := fmt.Sprintf("%s://%s:%s%s", scheme, hostname, port, path)
		resp, err := client.Get(healthURL)

		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("health check failed with status code: %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		backoff := defaultInitialBackoff * time.Duration(1<<attempt)
		if backoff > defaultMaxBackoff {
			backoff = defaultMaxBackoff
		}
		time.Sleep(backoff)
	}
	return fmt.Errorf("service unavailable after %d attempts: %v", retries, lastErr)
}
