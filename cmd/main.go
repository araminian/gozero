package main

import (
	"context"
	"log"
	"time"

	"github.com/araminian/gozero/internal/config"
	"github.com/araminian/gozero/internal/lock"
	"github.com/araminian/gozero/internal/metric"
	"github.com/araminian/gozero/internal/proxy"
	"github.com/araminian/gozero/internal/store"
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
	defaultProxyPort  = 8443
	defaultMetricPort = 9090
	defaultMetricPath = "/metrics"
	defaultTimeout    = 1 * time.Minute
	defaultBuffer     = 1000
	defaultRedisPort  = 6379
	defaultRedisAddr  = "localhost"
)

func main() {

	proxyPort := config.GetEnvOrDefaultInt("PROXY_PORT", defaultProxyPort)
	metricPort := config.GetEnvOrDefaultInt("METRIC_PORT", defaultMetricPort)
	metricPath := config.GetEnvOrDefaultString("METRIC_PATH", defaultMetricPath)
	requestTimeout := config.GetEnvOrDefaultDuration("REQUEST_TIMEOUT", defaultTimeout)
	buffer := config.GetEnvOrDefaultInt("REQUEST_BUFFER", defaultBuffer)
	redisAddr := config.GetEnvOrDefaultString("REDIS_ADDR", defaultRedisAddr)
	redisPort := config.GetEnvOrDefaultInt("REDIS_PORT", defaultRedisPort)

	ctx := context.Background()
	httpProxy, err := proxy.NewHTTPReverseProxy(proxy.WithListenPort(proxyPort), proxy.WithTimeout(requestTimeout), proxy.WithBufferSize(buffer))
	if err != nil {
		panic(err)
	}

	redisClient, err := store.NewRedisClient(ctx, store.WithRedisHost(redisAddr), store.WithRedisPort(redisPort))
	if err != nil {
		panic(err)
	}

	redisMutex := lock.NewRedisMutex(ctx, redisClient)

	metricServer, err := metric.NewFiberMetricExposer(metric.WithFiberMetricExposerPath(metricPath), metric.WithFiberMetricExposerPort(metricPort))
	if err != nil {
		panic(err)
	}
	server := &Server{
		proxy:  httpProxy,
		lock:   redisMutex,
		store:  redisClient,
		metric: metricServer,
	}

	go func() {
		if err := server.metric.Start(ctx, redisClient); err != nil {
			panic(err)
		}
	}()

	go func() {
		if err := server.Start(); err != nil {
			panic(err)
		}
	}()

	requests := server.Requests()

	hostMutex := server.lock.NewMutex("host")

	for request := range requests {
		log.Printf("Requests :=> %+v", request)

		err := hostMutex.TryLock()

		if err != nil {
			log.Printf("Error locking host mutex: %v", err)
			continue
		}

		err = server.store.ScaleUp(request.Host, 10, time.Minute*5)
		if err != nil {
			log.Printf("Error scaling up host: %v", err)
			continue
		}

		keyValues, err := server.store.GetAllScaleUpKeys()
		if err != nil {
			log.Printf("Error getting all scale up keys: %v", err)
			continue
		}

		log.Printf("Key values: %v", keyValues)

		hostMutex.Unlock()
	}
}
