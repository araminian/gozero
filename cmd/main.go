package main

import "github.com/araminian/gozero/internal/proxy"

type Server struct {
	proxy proxy.Proxier
}

func (s *Server) Start() error {
	return s.proxy.Start()
}

func main() {

	httpProxy, err := proxy.NewHTTPReverseProxy()
	if err != nil {
		panic(err)
	}

	server := &Server{
		proxy: httpProxy,
	}

	if err := server.Start(); err != nil {
		panic(err)
	}
}
