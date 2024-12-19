package metric

import (
	"context"
	"fmt"

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
	store Storer
	app   *fiber.App
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

func (m *FiberMetricExposer) Start(ctx context.Context, store Storer) error {
	m.store = store
	m.app = fiber.New()

	// Add route for base metrics path
	m.app.Get(m.path, m.exposeMetrics)

	// Keep existing route for service-specific metrics
	metricFullPath := fmt.Sprintf("%s%s", m.path, "/:svc")
	m.app.Get(metricFullPath, m.exposeMetrics)

	go func() {
		<-ctx.Done()
		_ = m.app.Shutdown()
	}()

	return m.app.Listen(fmt.Sprintf(":%d", m.port))
}

func (m *FiberMetricExposer) Shutdown(ctx context.Context) error {
	if m.app != nil {
		return m.app.ShutdownWithContext(ctx)
	}
	return nil
}

func (m *FiberMetricExposer) exposeMetrics(c *fiber.Ctx) error {
	svc := c.Params("svc")

	keys, err := m.store.GetAllScaleUpKeysValues()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if svc == "" {
		return c.JSON(keys)
	}

	if _, ok := keys[svc]; !ok {
		keys[svc] = "0"
	}

	svcValue := fiber.Map{
		"value": keys[svc],
	}

	return c.JSON(svcValue)
}
