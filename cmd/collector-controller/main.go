package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tyottodekiru/k8s-playground/pkg/queue"
)

func main() {
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379")

	redisQueue, err := queue.NewRedisQueue(redisURL)
	if err != nil {
		log.Fatalf("Failed to initialize Redis queue: %v", err)
	}
	defer redisQueue.Close()

	log.Println("Starting collector controller...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Collector controller shutting down...")
			return
		case <-ticker.C:
			if err := cleanupItems(ctx, redisQueue); err != nil {
				log.Printf("Error during cleanup: %v", err)
			}
		}
	}
}

func cleanupItems(ctx context.Context, redisQueue *queue.RedisQueue) error {
	allItems, err := redisQueue.GetAllItems(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	const terminatedGracePeriod = 5 * time.Minute

	for _, item := range allItems {
		// Collect expired items and mark them for shutdown
		if item.ShouldBeCollected() {
			log.Printf("Collecting expired item %s (expired at %v)", item.ID, item.ExpiresAt)

			item.Status = queue.StatusShutdown
			if err := redisQueue.UpdateItem(ctx, item); err != nil {
				log.Printf("Failed to update item %s status to shutdown: %v", item.ID, err)

				item.Status = queue.StatusError
				item.ErrorMessage = "Failed to mark for shutdown during collection"
				if updateErr := redisQueue.UpdateItem(ctx, item); updateErr != nil {
					log.Printf("Failed to update item %s status to error: %v", item.ID, updateErr)
				}
			}
			continue // This item is processed for this cycle
		}

		// Delete items that have been in the 'terminated' state for a while
		if item.Status == queue.StatusTerminated {
			if now.Sub(item.StatusUpdatedAt) > terminatedGracePeriod {
				log.Printf("Deleting old terminated item %s (terminated at %v)", item.ID, item.StatusUpdatedAt)
				if err := redisQueue.DeleteItem(ctx, item.ID); err != nil {
					log.Printf("Failed to delete terminated item %s: %v", item.ID, err)
				}
			}
		}
	}

	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
