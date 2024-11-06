package lock

import (
	"context"
	"testing"

	"github.com/araminian/gozero/internal/store"
)

func TestRedisMutex(t *testing.T) {
	ctx := context.Background()
	redisClient, err := store.NewRedisClient(ctx)
	if err != nil {
		t.Fatal(err)
	}

	redisMutex := NewRedisMutex(ctx, redisClient)

	mutex := redisMutex.NewMutex("foobar")

	t.Log(mutex)
	// Test Lock , Unlock
	err = mutex.Lock()
	if err != nil {
		t.Fatal(err)
	}

	unlocked, err := mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(unlocked)

	// Lock
	err = mutex.Lock()
	if err != nil {
		t.Fatal(err)
	}

	err = mutex.Lock()
	if err == nil {
		t.Fatal("Mutex should be locked")
	}
	t.Log(err)

	t.Log("Locked")
}
