package cache

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log"
	"os"
	"time"
)

var (
	Info  = log.New(os.Stdout, "INFO: ", 0)
	Warn  = log.New(os.Stdout, "WARNING: ", 0)
	Error = log.New(os.Stdout, "ERROR: ", 0)
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(host, port, password string) *RedisCache {
	Info.Println("Initializing Redis client")
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Password: password,
		DB:       0,
	})

	return &RedisCache{
		client: client,
	}
}

func (c *RedisCache) Set(key string, response []byte) error {
	ctx := context.Background()
	return c.client.Set(ctx, key, response, 24*time.Hour).Err()
}

func (c *RedisCache) Get(key string) ([]byte, error) {
	ctx := context.Background()
	return c.client.Get(ctx, key).Bytes()
}

func (c *RedisCache) GenerateCacheKey(path string) string {
	return "api:response:" + path
}
