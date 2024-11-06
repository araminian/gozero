package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisConfig func(*redisConfig) error

type redisConfig struct {
	Host *string
	Port *int
}

const (
	defaultHost      = "localhost"
	defaultPort      = 6379
	scaleUpKeyPrefix = "gozero:scale_up"
)

func WithRedisHost(host string) RedisConfig {
	return func(cfg *redisConfig) error {
		cfg.Host = &host
		return nil
	}
}

func WithRedisPort(port int) RedisConfig {
	return func(cfg *redisConfig) error {
		cfg.Port = &port
		return nil
	}
}

type RedisClient struct {
	Client *redis.Client
	Ctx    context.Context
}

func NewRedisClient(ctx context.Context, configs ...RedisConfig) (*RedisClient, error) {
	cfg := &redisConfig{}

	for _, config := range configs {
		if err := config(cfg); err != nil {
			return nil, err
		}
	}

	var (
		host = defaultHost
		port = defaultPort
	)

	if cfg.Host != nil {
		host = *cfg.Host
	}
	if cfg.Port != nil {
		port = *cfg.Port
	}

	client := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%d", host, port),
	})

	return &RedisClient{Client: client, Ctx: ctx}, nil
}

func (r *RedisClient) Ping() (bool, error) {
	result, err := r.Client.Ping(r.Ctx).Result()
	if err != nil {
		return false, err
	}

	return result == "PONG", nil
}

func (r *RedisClient) Close() error {
	return r.Client.Close()
}

func (r *RedisClient) ScaleUp(host string, scaleThreshold int, scaleDuration time.Duration) error {
	setScaleUpKey := fmt.Sprintf("%s:%s", scaleUpKeyPrefix, host)

	resultSet := r.Client.Set(r.Ctx, setScaleUpKey, scaleThreshold, scaleDuration)
	if resultSet.Err() != nil {
		return resultSet.Err()
	}

	return nil
}

func (r *RedisClient) ResetTimer(host string, scaleDuration time.Duration) error {
	setScaleUpKey := fmt.Sprintf("%s:%s", scaleUpKeyPrefix, host)

	return r.Client.Expire(r.Ctx, setScaleUpKey, scaleDuration).Err()
}

func (r *RedisClient) ScaleDown(host string) error {
	return nil
}

func (r *RedisClient) GetAllScaleUpKeys() ([]string, error) {
	return r.Client.Keys(r.Ctx, scaleUpKeyPrefix+":*").Result()
}

func (r *RedisClient) GetAllScaleUpKeysValues() (map[string]string, error) {
	keys, err := r.Client.Keys(r.Ctx, scaleUpKeyPrefix+":*").Result()
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return make(map[string]string), nil
	}

	pipe := r.Client.Pipeline()
	getCommands := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		getCommands[i] = pipe.Get(r.Ctx, key)
	}

	_, err = pipe.Exec(r.Ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(keys))
	for i, key := range keys {
		val, err := getCommands[i].Result()
		if err != nil {
			continue
		}
		result[key] = val
	}

	return result, nil
}
