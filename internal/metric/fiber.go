package metric

import (
	"context"
	"fmt"

	"github.com/araminian/gozero/internal/store"
	"github.com/gofiber/fiber/v2"
)

const (
	defaultFiberMetricExposerPort = 9090
	defaultFiberMetricExposerPath = "/metrics"
)

type fiberMetricExposerConfig struct {
	port *int
	path *string
}

type FiberMetricExposerConfig func(config *fiberMetricExposerConfig) error

func WithFiberMetricExposerPort(port int) FiberMetricExposerConfig {
	return func(config *fiberMetricExposerConfig) error {
		config.port = &port
		return nil
	}
}

func WithFiberMetricExposerPath(path string) FiberMetricExposerConfig {
	return func(config *fiberMetricExposerConfig) error {
		config.path = &path
		return nil
	}
}

type FiberMetricExposer struct {
	port  int
	path  string
	store store.Storer
}

func NewFiberMetricExposer(configs ...FiberMetricExposerConfig) (*FiberMetricExposer, error) {
	cfg := &fiberMetricExposerConfig{}
	for _, config := range configs {
		err := config(cfg)
		if err != nil {
			return nil, err
		}
	}

	var (
		port = defaultFiberMetricExposerPort
		path = defaultFiberMetricExposerPath
	)
	if cfg.port != nil {
		port = *cfg.port
	}
	if cfg.path != nil {
		path = *cfg.path
	}

	return &FiberMetricExposer{
		port: port,
		path: path,
	}, nil
}

func (m *FiberMetricExposer) Start(ctx context.Context, store store.Storer) error {
	m.store = store
	app := fiber.New()

	app.Get(m.path, m.exposeMetrics)

	return app.Listen(fmt.Sprintf(":%d", m.port))
}

func (m *FiberMetricExposer) exposeMetrics(c *fiber.Ctx) error {
	keys, err := m.store.GetAllScaleUpKeysValues()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(keys)
}
