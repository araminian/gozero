package metric

import (
	"context"

	"github.com/araminian/gozero/internal/store"
)

type MetricServer interface {
	Start(ctx context.Context, store store.Storer) error
	Shutdown(ctx context.Context) error
}
