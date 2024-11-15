package main

import (
	"context"
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
}

func (s *Server) Start() error {
	return s.proxy.Start()
}

func (s *Server) Requests() <-chan proxy.Requests {
	return s.proxy.Requests()
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

	ctx := context.Background()
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
	}

	go func() {
		if err := server.metric.Start(ctx, redisClient); err != nil {
			panic("failed to start metric server: " + err.Error())
		}
	}()

	go func() {
		if err := server.Start(); err != nil {
			panic("failed to start proxy server: " + err.Error())
		}
	}()

	requests := server.Requests()

	hostMutex := server.lock.NewMutex("host")

	for request := range requests {
		config.Log.Debugf("Requests :=> %+v", request)

		err := hostMutex.TryLock()

		if err != nil {
			config.Log.Errorf("Error locking mutex for host '%s': %+v", request.Host, err)
			continue
		}

		config.Log.Debugf("Scaling up host '%s' by %d for %s", request.Host, defaultScaleUpTarget, defaultScaleUpDuration)
		err = server.store.ScaleUp(request.Host, defaultScaleUpTarget, defaultScaleUpDuration)
		if err != nil {
			config.Log.Errorf("Error scaling up host '%s': %+v", request.Host, err)
			continue
		}

		keyValues, err := server.store.GetAllScaleUpKeys()
		if err != nil {
			config.Log.Errorf("Error getting all scale up keys: %+v", err)
			continue
		}

		config.Log.Debugf("Scale up keys: %+v", keyValues)

		hostMutex.Unlock()
	}
}
