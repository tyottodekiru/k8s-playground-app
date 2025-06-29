package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tyottodekiru/k8s-playground/pkg/k8s"
	"github.com/tyottodekiru/k8s-playground/pkg/queue"
)

var (
	dindImageBaseRepository string
	dindImageVersions       map[string]string
)

func main() {
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379")
	namespace := getEnv("NAMESPACE", "default")
	dindImageBaseRepository = getEnv("DIND_IMAGE_BASE_REPOSITORY", "tyottodekiru/dind")
	dindImageVersionsJSON := getEnv("DIND_IMAGE_VERSIONS_JSON", "{}")

	if err := json.Unmarshal([]byte(dindImageVersionsJSON), &dindImageVersions); err != nil {
		log.Fatalf("Failed to parse DIND_IMAGE_VERSIONS_JSON: %v. JSON was: %s", err, dindImageVersionsJSON)
	}
	if len(dindImageVersions) == 0 {
		log.Println("Warning: DIND_IMAGE_VERSIONS_JSON is empty or invalid. Generator will fail if K8s versions are not mapped.")
	}
	log.Printf("DinD Image Base Repository: %s", dindImageBaseRepository)
	log.Printf("DinD Image Versions Map: %+v", dindImageVersions)

	redisQueue, err := queue.NewRedisQueue(redisURL)
	if err != nil {
		log.Fatalf("Failed to initialize Redis queue: %v", err)
	}
	defer redisQueue.Close()

	k8sClient, err := k8s.NewClient()
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	log.Println("Starting generator controller...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Generator controller shutting down...")
			return
		case <-ticker.C:
			if err := processPendingItems(ctx, redisQueue, k8sClient, namespace); err != nil {
				log.Printf("Error processing pending items: %v", err)
			}
		}
	}
}

func processPendingItems(ctx context.Context, redisQueue *queue.RedisQueue, k8sClient *k8s.Client, namespace string) error {
	pendingItems, err := redisQueue.GetItemsByStatus(ctx, queue.StatusPending)
	if err != nil {
		return fmt.Errorf("failed to get pending items: %w", err)
	}

	for _, item := range pendingItems {
		if err := processItem(ctx, redisQueue, k8sClient, item, namespace); err != nil {
			log.Printf("Error processing item %s: %v", item.ID, err)

			item.Status = queue.StatusError
			item.ErrorMessage = err.Error()
			if updateErr := redisQueue.UpdateItem(ctx, item); updateErr != nil {
				log.Printf("Failed to update item %s status to error: %v", item.ID, updateErr)
			}
		}
	}

	return nil
}

func processItem(ctx context.Context, redisQueue *queue.RedisQueue, k8sClient *k8s.Client, item *queue.QueueItem, namespace string) error {
	item.Status = queue.StatusGenerating
	if err := redisQueue.UpdateItem(ctx, item); err != nil {
		return fmt.Errorf("failed to update item status to generating: %w", err)
	}

	workloadName := fmt.Sprintf("k8s-playground-%s", item.ID[:8])

	imageTag, ok := dindImageVersions[item.K8sVersion]
	if !ok {
		err := fmt.Errorf("unsupported k8s version for DinD image: %s. Check DIND_IMAGE_VERSIONS_JSON configuration. Available versions: %v", item.K8sVersion, getMapKeys(dindImageVersions))
		log.Println(err.Error())
		item.Status = queue.StatusError
		item.ErrorMessage = err.Error()
		if updateErr := redisQueue.UpdateItem(ctx, item); updateErr != nil {
			log.Printf("Failed to update item status to error after unsupported K8s version: %v", updateErr)
		}
		return err
	}
	dindImageName := fmt.Sprintf("%s:%s", dindImageBaseRepository, imageTag)
	log.Printf("Using DinD image: %s for K8s version %s (Item ID: %s)", dindImageName, item.K8sVersion, item.ID)

	workloadType := item.WorkloadType
	if workloadType == "" {
		workloadType = "statefulset" // Default to statefulset if not specified
	}
	log.Printf("Creating workload '%s' of type '%s' for item %s", workloadName, workloadType, item.ID)

	var podName string
	var err error

	// Get the NFS Service ClusterIP to bypass node DNS issues
	nfsServerIP, err := k8sClient.GetServiceClusterIP(ctx, "k8s-playground-nfs-server", namespace)
	if err != nil {
		return fmt.Errorf("failed to get nfs server service IP: %w", err)
	}
	log.Printf("Found NFS Server ClusterIP: %s", nfsServerIP)

	// Create a per-user subdirectory on the NFS server
	nfsSubPath, err := k8sClient.EnsureNFSDirectory(ctx, namespace, item.Owner)
	if err != nil {
		return fmt.Errorf("failed to ensure nfs directory for owner %s: %w", item.Owner, err)
	}
	log.Printf("Using NFS subpath '%s' for item %s", nfsSubPath, item.ID)

	if workloadType == "deployment" {
		_, err = k8sClient.CreateDinDDeployment(ctx, workloadName, namespace, dindImageName, nfsServerIP, nfsSubPath)
	} else {
		pvcSize := getEnv("DIND_PVC_SIZE", "10Gi")
		podName, err = k8sClient.CreateDinDStatefulSet(ctx, workloadName, namespace, dindImageName, pvcSize, nfsServerIP, nfsSubPath)
	}

	if err != nil {
		return fmt.Errorf("failed to create DinD workload with image %s: %w", dindImageName, err)
	}
	item.PodID = workloadName

	log.Printf("Created workload %s for item %s", workloadName, item.ID)

	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for pod to be running for workload %s", workloadName)
		case <-ticker.C:
			// Resolve pod name if it's not yet known (for deployments)
			if podName == "" && workloadType == "deployment" {
				resolvedPodName, resolveErr := k8sClient.GetPodNameForWorkload(ctx, workloadName, namespace)
				if resolveErr != nil {
					log.Printf("Waiting for pod to be created for workload %s...", workloadName)
					continue
				}
				podName = resolvedPodName
				log.Printf("Resolved pod name for workload %s: %s", workloadName, podName)
			}
			if podName == "" {
				continue // Still waiting for pod to be created
			}

			running, err := k8sClient.IsPodRunning(ctx, podName, namespace)
			if err != nil {
				log.Printf("Failed to check pod status for %s, assuming creation failed: %v", podName, err)
				return fmt.Errorf("failed to check pod status for %s: %w", podName, err)
			}

			if running {
				item.Status = queue.StatusAvailable
				if err := redisQueue.UpdateItem(ctx, item); err != nil {
					return fmt.Errorf("failed to update item status to available: %w", err)
				}
				log.Printf("Pod %s is running, item %s is now available", podName, item.ID)
				return nil
			}
			currentPod, getErr := k8sClient.GetPod(ctx, podName, namespace)
			if getErr == nil {
				log.Printf("Pod %s is still not running. Current status: %s. Waiting...", podName, currentPod.Status.Phase)
			} else {
				log.Printf("Pod %s is still not running. Error getting current status: %v. Waiting...", podName, getErr)
			}
		}
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
