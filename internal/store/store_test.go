package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPing(t *testing.T) {
	redisClient, err := NewRedisClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ok, err := redisClient.Ping()
	if err != nil {
		t.Fatal(err)
	}

	assert.True(t, ok)

	err = redisClient.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestScaleUp(t *testing.T) {

	redisClient, err := NewRedisClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	err = redisClient.ScaleUp("foobar", 10, time.Second*300)
	if err != nil {
		t.Fatal(err)
	}

	gotKeys, err := redisClient.GetAllScaleUpKeys()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Got %v keys", gotKeys)

	key := fmt.Sprintf("%s:foobar", scaleUpKeyPrefix)
	wantedKeys := []string{key}
	assert.Equal(t, wantedKeys, gotKeys)

	gotKeysValues, err := redisClient.GetAllScaleUpKeysValues()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Got %+v values", gotKeysValues)

	wantedKeysValues := map[string]string{
		key: "10",
	}
	assert.Equal(t, wantedKeysValues, gotKeysValues)

	err = redisClient.Close()
	if err != nil {
		t.Fatal(err)
	}
}
