package main

import (
	"log"

	"github.com/araminian/gozero/internal/proxy"
)

type Server struct {
	proxy proxy.Proxier
}

func (s *Server) Start() error {
	return s.proxy.Start()
}

func (s *Server) Requests() <-chan proxy.Requests {
	return s.proxy.Requests()
}

func main() {

	httpProxy, err := proxy.NewHTTPReverseProxy()
	if err != nil {
		panic(err)
	}

	server := &Server{
		proxy: httpProxy,
	}

	go func() {
		if err := server.Start(); err != nil {
			panic(err)
		}
	}()

	requests := server.Requests()

	for request := range requests {
		log.Printf("Requests :=> %+v", request)
	}
}
