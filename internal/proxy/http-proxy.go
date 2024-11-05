package proxy

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type HTTPReverseProxyConfig func(*httpReverseProxyConfig) error

const (
	defaultTimeout = 10 * time.Minute
	defaultPort    = 8443
)

type httpReverseProxyConfig struct {
	Timeout    *time.Duration
	ListenPort *int
}

type HTTPReverseProxy struct {
	Timeout    time.Duration
	ListenPort int
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
		timeout    time.Duration
		listenPort int
	)
	timeout = defaultTimeout
	listenPort = defaultPort

	if cfg.Timeout != nil {
		timeout = *cfg.Timeout
	}

	if cfg.ListenPort != nil {
		listenPort = *cfg.ListenPort
	}

	return &HTTPReverseProxy{
		Timeout:    timeout,
		ListenPort: listenPort,
	}, nil
}

func (p *HTTPReverseProxy) Start() error {
	proxy := &httputil.ReverseProxy{
		Director: p.httpDirector,
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
	err := server.ListenAndServeTLS("server.crt", "server.key")
	//err := server.ListenAndServe()
	if err != nil {
		return err
	}

	return nil
}

func (p *HTTPReverseProxy) httpDirector(req *http.Request) {
	// targetHost := req.Host

	targetHost := "www.trivago.com"
	originalScheme := req.URL.Scheme
	if originalScheme == "" {
		if req.TLS != nil {
			originalScheme = "https"
		} else {
			originalScheme = "http"
		}
	}

	//originalScheme = "https"

	targetURL, err := url.Parse(fmt.Sprintf("%s://%s", originalScheme, targetHost))
	if err != nil {
		log.Printf("Error parsing target URL: %v", err)
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
