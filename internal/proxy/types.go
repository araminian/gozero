package proxy

import (
	"net/http"
)

// HTTPReverseProxyConfig is a function type for configuring the proxy
type HTTPReverseProxyConfig func(*httpReverseProxyConfig) error

// httpReverseProxyConfig holds the configuration for the HTTP reverse proxy
type httpReverseProxyConfig struct {
	listenPort    *int
	requestBuffer *int
}

// WithBufferSize sets the buffer size for the proxy
func WithBufferSize(buffer int) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.requestBuffer = &buffer
		return nil
	}
}

// WithListenPort sets the listen port for the proxy
func WithListenPort(port int) HTTPReverseProxyConfig {
	return func(cfg *httpReverseProxyConfig) error {
		cfg.listenPort = &port
		return nil
	}
}

// HTTPReverseProxy is the main proxy structure
type HTTPReverseProxy struct {
	listenPort        int
	httpServer        *http.Server
	requestBufferSize int
	requestsCh        chan Requests
}

// Requests represents a proxy request
type Requests struct {
	Host string
	Path string
}
