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
	"strconv"
	"strings"
	"time"

	"github.com/eapache/go-resiliency/retrier"
	"go.uber.org/zap"
	"golang.org/x/net/http2"

	"github.com/araminian/gozero/internal/config"
)

// retryRoundTripper implements retry logic for HTTP requests
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

// conditionalTransport handles both HTTP/1.1 and HTTP/2 transports
type conditionalTransport struct {
	h2Transport *http2.Transport
	h1Transport *http.Transport
}

// newConditionalTransport creates a new transport that can handle both HTTP/1.1 and HTTP/2
func newConditionalTransport() *conditionalTransport {
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

	return &conditionalTransport{
		h2Transport: http2Transport,
		h1Transport: http1Transport,
	}
}

func (t *conditionalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Proto == "HTTP/2.0" {
		config.Log.Debug("Protocol: HTTP/2.0, Using HTTP/2 transport for request", zap.String("url", req.URL.String()))
		return t.h2Transport.RoundTrip(req)
	}
	config.Log.Debug("Protocol: HTTP/1.1, Using HTTP/1.1 transport for request", zap.String("url", req.URL.String()))
	return t.h1Transport.RoundTrip(req)
}
