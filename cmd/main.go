package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/araminian/gozero/internal/config"
	"github.com/araminian/gozero/internal/metric"
	"github.com/araminian/gozero/internal/proxy"
	"github.com/araminian/gozero/internal/store"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Storer interface {
	Close() error
	GetAllScaleUpKeys() ([]string, error)
	ScaleUp(host string, scaleThreshold int, scaleDuration time.Duration) error
}

type MetricServer interface {
	Start(ctx context.Context, store metric.Storer) error
	Shutdown(ctx context.Context) error
}

type Server struct {
	proxy  proxy.Proxier
	store  Storer
	metric MetricServer
	done   chan struct{}
}

const (
	defaultProxyPort       = 8443
	defaultMetricPort      = 9090
	defaultMetricPath      = "/metrics"
	defaultBuffer          = 1000
	defaultRedisPort       = 6379
	defaultRedisAddr       = "localhost"
	defaultLogLevel        = "info"
	defaultScaleUpTarget   = 10
	defaultScaleUpDuration = 5 * time.Minute
)

func main() {

	proxyPort := config.GetEnvOrDefaultInt("PROXY_PORT", defaultProxyPort)
	metricPort := config.GetEnvOrDefaultInt("METRIC_PORT", defaultMetricPort)
	metricPath := config.GetEnvOrDefaultString("METRIC_PATH", defaultMetricPath)
	buffer := config.GetEnvOrDefaultInt("REQUEST_BUFFER", defaultBuffer)
	redisAddr := config.GetEnvOrDefaultString("REDIS_ADDR", defaultRedisAddr)
	redisPort := config.GetEnvOrDefaultInt("REDIS_PORT", defaultRedisPort)
	logLevel := config.GetEnvOrDefaultString("LOG_LEVEL", defaultLogLevel)

	logLevelObj, err := zapcore.ParseLevel(logLevel)
	if err != nil {
		logLevelObj = zapcore.InfoLevel
	}
	config.InitLogger(logLevelObj)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpProxy, err := proxy.NewHTTPReverseProxy(proxy.WithListenPort(proxyPort), proxy.WithBufferSize(buffer))
	if err != nil {
		panic("failed to create http proxy: " + err.Error())
	}

	redisClient, err := store.NewRedisClient(ctx, store.WithRedisHost(redisAddr), store.WithRedisPort(redisPort))
	if err != nil {
		panic("failed to create redis client: " + err.Error())
	}

	metricServer, err := metric.NewFiberMetricExposer(metric.WithFiberMetricExposerPath(metricPath), metric.WithFiberMetricExposerPort(metricPort))
	if err != nil {
		panic("failed to create metric server: " + err.Error())
	}
	server := &Server{
		proxy:  httpProxy,
		store:  redisClient,
		metric: metricServer,
		done:   make(chan struct{}),
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(2)

	// Start metric server
	go func() {
		defer func() {
			wg.Done()
			config.Log.Info("Metric server shutdown complete")
		}()
		if err := server.metric.Start(ctx, redisClient); err != nil && !errors.Is(err, context.Canceled) {
			config.Log.Error("metric server error", zap.Error(err))
		}
	}()

	// Start proxy server
	go func() {
		defer func() {
			wg.Done()
			config.Log.Info("Proxy server shutdown complete")
		}()
		if err := server.proxy.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			config.Log.Error("proxy server error", zap.Error(err))
		}
	}()

	go server.processRequests(ctx)

	<-sigChan
	config.Log.Info("Shutting down servers...")

	cancel()
	close(server.done)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-shutdownCtx.Done():
		config.Log.Warn("Shutdown timed out")
	case <-done:
		config.Log.Info("All servers shut down successfully")
	}

	if err := server.store.Close(); err != nil {
		config.Log.Error("Error closing store", zap.Error(err))
	}

	config.Log.Info("Shutdown complete")
}

func (s *Server) processRequests(ctx context.Context) {
	requests := s.proxy.Requests()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case request, ok := <-requests:
			if !ok {
				// Channel was closed
				return
			}

			config.Log.Debug("Received request", zap.Any("request", request))

			config.Log.Debug("Scaling up host", zap.String("host", request.Host), zap.Int("target", defaultScaleUpTarget), zap.Duration("duration", defaultScaleUpDuration))
			err := s.store.ScaleUp(request.Host, defaultScaleUpTarget, defaultScaleUpDuration)
			if err != nil {
				config.Log.Error("Error scaling up host", zap.String("host", request.Host), zap.Error(err))
				continue
			}

			keyValues, err := s.store.GetAllScaleUpKeys()
			if err != nil {
				config.Log.Error("Error getting all scale up keys", zap.Error(err))
				continue
			}

			config.Log.Debug("Scale up keys", zap.Any("keys", keyValues))
		}
	}
}
