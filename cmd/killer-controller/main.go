package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tyottodekiru/k8s-playground/pkg/k8s"
	"github.com/tyottodekiru/k8s-playground/pkg/queue"
)

func main() {
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379")
	namespace := getEnv("NAMESPACE", "default")

	redisQueue, err := queue.NewRedisQueue(redisURL)
	if err != nil {
		log.Fatalf("Failed to initialize Redis queue: %v", err)
	}
	defer redisQueue.Close()

	k8sClient, err := k8s.NewClient()
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	log.Println("Starting killer controller...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Killer controller shutting down...")
			return
		case <-ticker.C:
			if err := processShutdownItems(ctx, redisQueue, k8sClient, namespace); err != nil {
				log.Printf("Error processing shutdown items: %v", err)
			}
		}
	}
}

func processShutdownItems(ctx context.Context, redisQueue *queue.RedisQueue, k8sClient *k8s.Client, namespace string) error {
	shutdownItems, err := redisQueue.GetItemsByStatus(ctx, queue.StatusShutdown)
	if err != nil {
		return fmt.Errorf("failed to get shutdown items: %w", err)
	}

	for _, item := range shutdownItems {
		if err := processShutdownItem(ctx, redisQueue, k8sClient, item, namespace); err != nil {
			log.Printf("Error processing shutdown item %s: %v", item.ID, err)

			item.Status = queue.StatusError
			item.ErrorMessage = err.Error()
			if updateErr := redisQueue.UpdateItem(ctx, item); updateErr != nil {
				log.Printf("Failed to update item status to error: %v", updateErr)
			}
		}
	}

	return nil
}

func processShutdownItem(ctx context.Context, redisQueue *queue.RedisQueue, k8sClient *k8s.Client, item *queue.QueueItem, namespace string) error {
	// Mark as Terminated first, so we don't re-process it if deletion fails
	item.Status = queue.StatusTerminated
	if err := redisQueue.UpdateItem(ctx, item); err != nil {
		return fmt.Errorf("failed to update item status to terminating: %w", err)
	}

	if item.PodID != "" { // PodID now holds the StatefulSet or Deployment name
		log.Printf("Deleting workload %s (type: %s) for item %s", item.PodID, item.WorkloadType, item.ID)

		var err error
		if item.WorkloadType == "deployment" {
			err = k8sClient.DeleteDinDDeployment(ctx, item.PodID, namespace)
		} else {
			// Default to statefulset for backward compatibility
			err = k8sClient.DeleteDinDStatefulSet(ctx, item.PodID, namespace)
		}

		if err != nil {
			log.Printf("Warning: Failed to delete workload %s: %v", item.PodID, err)
			// Even if deletion fails, we keep the status as Terminated
		}
	}

	log.Printf("Successfully processed termination for item %s", item.ID)
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
