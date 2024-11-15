package proxy

import (
	"context"
)

type Proxier interface {
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Requests() <-chan Requests
}

type Requests struct {
	Host string
	Path string
}
