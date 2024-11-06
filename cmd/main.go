package main

import (
	"context"
	"log"
	"time"

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

func main() {
	ctx := context.Background()
	httpProxy, err := proxy.NewHTTPReverseProxy()
	if err != nil {
		panic(err)
	}

	redisClient, err := store.NewRedisClient(ctx)
	if err != nil {
		panic(err)
	}

	redisMutex := lock.NewRedisMutex(ctx, redisClient)

	metricServer, err := metric.NewFiberMetricExposer()
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

		err = server.store.ScaleUp(request.Host, 10, time.Minute*10)
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
