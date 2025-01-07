package store

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type testRedis struct {
	container testcontainers.Container
	port      string
	host      string
}

func setupRedis(t *testing.T) *testRedis {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:latest",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)

	mappedPort, err := container.MappedPort(ctx, "6379")
	require.NoError(t, err)

	return &testRedis{
		container: container,
		port:      mappedPort.Port(),
		host:      host,
	}
}

func (tr *testRedis) GetPort() int {
	port, _ := strconv.Atoi(tr.port)
	return port
}

func (tr *testRedis) Cleanup(ctx context.Context) error {
	return tr.container.Terminate(ctx)
}

func TestPing(t *testing.T) {
	ctx := context.Background()
	redis := setupRedis(t)
	defer redis.Cleanup(ctx)

	t.Logf("Connecting to Redis at %s:%d", redis.host, redis.GetPort())

	redisClient, err := NewRedisClient(ctx,
		WithRedisHost(redis.host),
		WithRedisPort(redis.GetPort()),
	)
	require.NoError(t, err)
	defer redisClient.Close()

	ok, err := redisClient.Ping()
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestScaleUp(t *testing.T) {
	ctx := context.Background()
	redis := setupRedis(t)
	defer redis.Cleanup(ctx)

	t.Logf("Connecting to Redis at %s:%d", redis.host, redis.GetPort())

	redisClient, err := NewRedisClient(ctx,
		WithRedisHost(redis.host),
		WithRedisPort(redis.GetPort()),
	)
	require.NoError(t, err)
	defer redisClient.Close()

	err = redisClient.ScaleUp("foobar", 10, time.Second*300)
	if err != nil {
		t.Fatal(err)
	}

	gotKeys, err := redisClient.GetAllScaleUpKeys()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Got %v keys", gotKeys)

	wantedKeys := []string{fmt.Sprintf("%s:foobar", scaleUpKeyPrefix)}
	assert.Equal(t, wantedKeys, gotKeys)

	gotKeysValues, err := redisClient.GetAllScaleUpKeysValues()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Got %+v values", gotKeysValues)

	wantedKeysValues := map[string]string{
		"foobar": "10",
	}
	assert.Equal(t, wantedKeysValues, gotKeysValues)

	err = redisClient.Close()
	if err != nil {
		t.Fatal(err)
	}
}
