package lock

import (
	"context"

	"github.com/araminian/gozero/internal/store"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
)

type RedisMutex struct {
	redisClient *store.RedisClient
	rs          *redsync.Redsync
	ctx         context.Context
}

func NewRedisMutex(ctx context.Context, redisClient *store.RedisClient) *RedisMutex {
	pool := goredis.NewPool(redisClient.Client)
	return &RedisMutex{
		redisClient: redisClient,
		rs:          redsync.New(pool),
		ctx:         ctx,
	}
}

func (r *RedisMutex) NewMutex(name string) Mutexer {
	return r.rs.NewMutex(name)
}
