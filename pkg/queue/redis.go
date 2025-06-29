package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const (
	QueueKey = "k8s_playground_queue"
)

type RedisQueue struct {
	Client *redis.Client
}

func NewRedisQueue(redisURL string) (*RedisQueue, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisQueue{Client: client}, nil
}

func (r *RedisQueue) AddItem(ctx context.Context, item *QueueItem) error {
	if item.ID == "" {
		item.ID = uuid.New().String()
	}

	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal queue item: %w", err)
	}

	return r.Client.HSet(ctx, QueueKey, item.ID, data).Err()
}

func (r *RedisQueue) GetItem(ctx context.Context, id string) (*QueueItem, error) {
	data, err := r.Client.HGet(ctx, QueueKey, id).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("item not found")
		}
		return nil, fmt.Errorf("failed to get queue item: %w", err)
	}

	var item QueueItem
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		return nil, fmt.Errorf("failed to unmarshal queue item: %w", err)
	}

	return &item, nil
}

func (r *RedisQueue) UpdateItem(ctx context.Context, item *QueueItem) error {
	item.StatusUpdatedAt = time.Now()

	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal queue item: %w", err)
	}

	return r.Client.HSet(ctx, QueueKey, item.ID, data).Err()
}

func (r *RedisQueue) GetAllItems(ctx context.Context) ([]*QueueItem, error) {
	data, err := r.Client.HGetAll(ctx, QueueKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get all queue items: %w", err)
	}

	items := make([]*QueueItem, 0, len(data))
	for _, itemData := range data {
		var item QueueItem
		if err := json.Unmarshal([]byte(itemData), &item); err != nil {
			continue // Skip invalid items
		}
		items = append(items, &item)
	}

	return items, nil
}

func (r *RedisQueue) GetItemsByStatus(ctx context.Context, status QueueStatus) ([]*QueueItem, error) {
	allItems, err := r.GetAllItems(ctx)
	if err != nil {
		return nil, err
	}

	var filteredItems []*QueueItem
	for _, item := range allItems {
		if item.Status == status {
			filteredItems = append(filteredItems, item)
		}
	}

	return filteredItems, nil
}

func (r *RedisQueue) GetItemsByOwner(ctx context.Context, owner string) ([]*QueueItem, error) {
	allItems, err := r.GetAllItems(ctx)
	if err != nil {
		return nil, err
	}

	var filteredItems []*QueueItem
	for _, item := range allItems {
		if item.Owner == owner {
			filteredItems = append(filteredItems, item)
		}
	}

	return filteredItems, nil
}

func (r *RedisQueue) DeleteItem(ctx context.Context, id string) error {
	return r.Client.HDel(ctx, QueueKey, id).Err()
}

func (r *RedisQueue) Close() error {
	return r.Client.Close()
}
