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
	"github.com/araminian/gozero/internal/lock"
	"github.com/araminian/gozero/internal/metric"
	"github.com/araminian/gozero/internal/proxy"
	"github.com/araminian/gozero/internal/store"
	"github.com/sirupsen/logrus"
)

type Server struct {
	proxy  proxy.Proxier
	lock   lock.Locker
	store  store.Storer
	metric metric.MetricServer
	done   chan struct{}
}

const (
	defaultProxyPort       = 8443
	defaultMetricPort      = 9090
	defaultMetricPath      = "/metrics"
	defaultTimeout         = 1 * time.Minute
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
	requestTimeout := config.GetEnvOrDefaultDuration("REQUEST_TIMEOUT", defaultTimeout)
	buffer := config.GetEnvOrDefaultInt("REQUEST_BUFFER", defaultBuffer)
	redisAddr := config.GetEnvOrDefaultString("REDIS_ADDR", defaultRedisAddr)
	redisPort := config.GetEnvOrDefaultInt("REDIS_PORT", defaultRedisPort)
	logLevel := config.GetEnvOrDefaultString("LOG_LEVEL", defaultLogLevel)

	config.Log = logrus.New()
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	config.Log.SetLevel(level)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpProxy, err := proxy.NewHTTPReverseProxy(proxy.WithListenPort(proxyPort), proxy.WithTimeout(requestTimeout), proxy.WithBufferSize(buffer))
	if err != nil {
		panic("failed to create http proxy: " + err.Error())
	}

	redisClient, err := store.NewRedisClient(ctx, store.WithRedisHost(redisAddr), store.WithRedisPort(redisPort))
	if err != nil {
		panic("failed to create redis client: " + err.Error())
	}

	redisMutex := lock.NewRedisMutex(ctx, redisClient)

	metricServer, err := metric.NewFiberMetricExposer(metric.WithFiberMetricExposerPath(metricPath), metric.WithFiberMetricExposerPort(metricPort))
	if err != nil {
		panic("failed to create metric server: " + err.Error())
	}
	server := &Server{
		proxy:  httpProxy,
		lock:   redisMutex,
		store:  redisClient,
		metric: metricServer,
		done:   make(chan struct{}),
	}

	// Channel to catch shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start servers in goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	// Start metric server
	go func() {
		defer func() {
			wg.Done()
			config.Log.Info("Metric server shutdown complete")
		}()
		if err := server.metric.Start(ctx, redisClient); err != nil && !errors.Is(err, context.Canceled) {
			config.Log.Errorf("metric server error: %+v", err)
		}
	}()

	// Start proxy server
	go func() {
		defer func() {
			wg.Done()
			config.Log.Info("Proxy server shutdown complete")
		}()
		if err := server.proxy.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			config.Log.Errorf("proxy server error: %+v", err)
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
		config.Log.Errorf("Error closing store: %+v", err)
	}

	config.Log.Info("Shutdown complete")
}

func (s *Server) processRequests(ctx context.Context) {
	requests := s.proxy.Requests()
	hostMutex := s.lock.NewMutex("host")

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

			config.Log.Debugf("Requests :=> %+v", request)

			err := hostMutex.TryLock()
			if err != nil {
				config.Log.Errorf("Error locking mutex for host '%s': %+v", request.Host, err)
				continue
			}

			config.Log.Debugf("Scaling up host '%s' by %d for %s", request.Host, defaultScaleUpTarget, defaultScaleUpDuration)
			err = s.store.ScaleUp(request.Host, defaultScaleUpTarget, defaultScaleUpDuration)
			if err != nil {
				config.Log.Errorf("Error scaling up host '%s': %+v", request.Host, err)
				hostMutex.Unlock()
				continue
			}

			keyValues, err := s.store.GetAllScaleUpKeys()
			if err != nil {
				config.Log.Errorf("Error getting all scale up keys: %+v", err)
				hostMutex.Unlock()
				continue
			}

			config.Log.Debugf("Scale up keys: %+v", keyValues)
			hostMutex.Unlock()
		}
	}
}
