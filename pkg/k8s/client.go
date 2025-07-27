package k8s

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// TerminalSize represents terminal dimensions
type TerminalSize struct {
	Width  uint16
	Height uint16
}

// TerminalSizeQueue handles terminal resize events
type TerminalSizeQueue interface {
	Next() *TerminalSize
}

// Client wraps Kubernetes client functionality
type Client struct {
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
}

// NewClient creates a new Kubernetes client
func NewClient() (*Client, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &Client{
		clientset:  clientset,
		restConfig: config,
	}, nil
}

// GetClientset returns the underlying Kubernetes clientset
func (c *Client) GetClientset() *kubernetes.Clientset {
	return c.clientset
}

// GetRestConfig returns the underlying REST config
func (c *Client) GetRestConfig() *rest.Config {
	return c.restConfig
}

// getKubeConfig gets Kubernetes configuration
func getKubeConfig() (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		kubeconfig = filepath.Join(homeDir, ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	return config, nil
}

// sanitizeName sanitizes a string to be a valid directory name.
func sanitizeName(name string) string {
	sanitized := strings.ToLower(name)
	reg := regexp.MustCompile("[^a-z0-9-]+")
	sanitized = reg.ReplaceAllString(sanitized, "-")
	sanitized = strings.Trim(sanitized, "-")
	if len(sanitized) == 0 {
		return "invalid-name"
	}
	return sanitized
}

// GetServiceClusterIP gets the ClusterIP of a Service.
func (c *Client) GetServiceClusterIP(ctx context.Context, name, namespace string) (string, error) {
	service, err := c.clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get service %s in namespace %s: %w", name, namespace, err)
	}
	if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == "None" {
		return "", fmt.Errorf("service %s does not have a ClusterIP", name)
	}
	return service.Spec.ClusterIP, nil
}

// EnsureNFSDirectory creates a per-user directory on the NFS server.
func (c *Client) EnsureNFSDirectory(ctx context.Context, namespace, ownerID string) (string, error) {
	nfsServerPodName := "k8s-playground-nfs-server-0"
	dirName := sanitizeName(ownerID)
	dirPath := filepath.Join("/exports", dirName)

	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(nfsServerPodName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"mkdir", "-p", dirPath},
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create SPDY executor for nfs-server: %w", err)
	}

	var stdout, stderr strings.Builder
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		log.Printf("mkdir stderr: %s", stderr.String())
		return "", fmt.Errorf("failed to exec mkdir on nfs-server: %w", err)
	}
	return dirName, nil
}

// CreateDinDStatefulSet creates a headless service and a StatefulSet for the playground
func (c *Client) CreateDinDStatefulSet(ctx context.Context, name, namespace, dindImageName, pvcSize, nfsServerIP, nfsSubPath string) (string, error) {
	headlessSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": "k8s-playground", "component": "dind-environment", "owner-id": name},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  map[string]string{"app": "k8s-playground-sts", "owner-id": name},
		},
	}
	_, err := c.clientset.CoreV1().Services(namespace).Create(ctx, headlessSvc, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("failed to create headless service: %w", err)
	}

	privileged := true
	replicas := int32(1)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": "k8s-playground", "component": "dind-environment", "owner-id": name},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "k8s-playground-sts", "owner-id": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "k8s-playground-sts", "component": "dind-environment", "owner-id": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "dind",
							Image:           dindImageName,
							SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
							Env:             []corev1.EnvVar{{Name: "DOCKER_TLS_CERTDIR", Value: ""}},
							Ports:           []corev1.ContainerPort{{ContainerPort: 2375, Protocol: corev1.ProtocolTCP}},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "docker-graph-storage", MountPath: "/var/lib/docker"},
								{Name: "tmp", MountPath: "/tmp"},
								{
									Name:      "nfs-user-share",
									MountPath: "/root/share",
									SubPath:   nfsSubPath,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi"), corev1.ResourceCPU: resource.MustParse("100m")},
								Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi"), corev1.ResourceCPU: resource.MustParse("1000m")},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"docker", "ps"}}},
								InitialDelaySeconds: 15, TimeoutSeconds: 5, PeriodSeconds: 10, FailureThreshold: 3,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"docker", "ps"}}},
								InitialDelaySeconds: 30, TimeoutSeconds: 5, PeriodSeconds: 20, FailureThreshold: 3,
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{
							Name: "nfs-user-share",
							VolumeSource: corev1.VolumeSource{
								NFS: &corev1.NFSVolumeSource{
									Server: nfsServerIP,
									Path:   "/",
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
					DNSPolicy:     corev1.DNSClusterFirst,
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "docker-graph-storage"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(pvcSize)},
						},
					},
				},
			},
		},
	}

	_, err = c.clientset.AppsV1().StatefulSets(namespace).Create(ctx, sts, metav1.CreateOptions{})
	if err != nil {
		_ = c.clientset.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		return "", fmt.Errorf("failed to create statefulset: %w", err)
	}

	podName := fmt.Sprintf("%s-0", name)
	return podName, nil
}

// CreateDinDDeployment: Creates a Service and a Deployment with ephemeral storage
func (c *Client) CreateDinDDeployment(ctx context.Context, name, namespace, dindImageName, nfsServerIP, nfsSubPath string) (string, error) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": "k8s-playground", "component": "dind-environment", "owner-id": name},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "k8s-playground-dep", "owner-id": name},
			Ports:    []corev1.ServicePort{{Name: "docker", Port: 2375, TargetPort: intstr.FromInt(2375)}},
		},
	}
	_, err := c.clientset.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("failed to create service for deployment: %w", err)
	}

	privileged := true
	replicas := int32(1)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"app": "k8s-playground", "component": "dind-environment", "owner-id": name}},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "k8s-playground-dep", "owner-id": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "k8s-playground-dep", "component": "dind-environment", "owner-id": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            "dind",
						Image:           dindImageName,
						SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
						Env:             []corev1.EnvVar{{Name: "DOCKER_TLS_CERTDIR", Value: ""}},
						Ports:           []corev1.ContainerPort{{ContainerPort: 2375, Protocol: corev1.ProtocolTCP}},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "docker-graph-storage", MountPath: "/var/lib/docker"},
							{Name: "tmp", MountPath: "/tmp"},
							{
								Name:      "nfs-user-share",
								MountPath: "/root/share",
								SubPath:   nfsSubPath,
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi"), corev1.ResourceCPU: resource.MustParse("100m")},
							Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi"), corev1.ResourceCPU: resource.MustParse("1000m")},
						},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"docker", "ps"}}}, InitialDelaySeconds: 15, TimeoutSeconds: 5, PeriodSeconds: 10, FailureThreshold: 3},
						LivenessProbe:  &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"docker", "ps"}}}, InitialDelaySeconds: 30, TimeoutSeconds: 5, PeriodSeconds: 20, FailureThreshold: 3},
					}},
					Volumes: []corev1.Volume{
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "docker-graph-storage", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{
							Name: "nfs-user-share",
							VolumeSource: corev1.VolumeSource{
								NFS: &corev1.NFSVolumeSource{
									Server: nfsServerIP,
									Path:   "/",
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
					DNSPolicy:     corev1.DNSClusterFirst,
				},
			},
		},
	}

	_, err = c.clientset.AppsV1().Deployments(namespace).Create(ctx, dep, metav1.CreateOptions{})
	if err != nil {
		_ = c.clientset.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		return "", fmt.Errorf("failed to create deployment: %w", err)
	}

	return name, nil
}


func (c *Client) GetPod(ctx context.Context, name, namespace string) (*corev1.Pod, error) {
	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod %s in namespace %s: %w", name, namespace, err)
	}
	return pod, nil
}

func (c *Client) DeleteDinDStatefulSet(ctx context.Context, name, namespace string) error {
	deletePolicy := metav1.DeletePropagationForeground

	err := c.clientset.AppsV1().StatefulSets(namespace).Delete(ctx, name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete statefulset %s: %w", name, err)
	}

	err = c.clientset.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete service %s: %w", name, err)
	}

	pvcName := fmt.Sprintf("docker-graph-storage-%s-0", name)
	err = c.clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvcName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete pvc %s: %w", pvcName, err)
	}

	return nil
}

func (c *Client) DeleteDinDDeployment(ctx context.Context, name, namespace string) error {
	deletePolicy := metav1.DeletePropagationForeground
	if err := c.clientset.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete deployment %s: %w", name, err)
	}
	if err := c.clientset.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete service %s: %w", name, err)
	}
	return nil
}

func (c *Client) GetPodNameForWorkload(ctx context.Context, workloadName, namespace string) (string, error) {
	podList, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=k8s-playground-dep,owner-id=%s", workloadName),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for workload %s: %w", workloadName, err)
	}
	if len(podList.Items) == 0 {
		return "", fmt.Errorf("no pods found for workload %s", workloadName)
	}
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
			return pod.Name, nil
		}
	}
	return podList.Items[0].Name, nil
}

func (c *Client) IsPodRunning(ctx context.Context, name, namespace string) (bool, error) {
	pod, err := c.GetPod(ctx, name, namespace)
	if err != nil {
		return false, err
	}

	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
		log.Printf("[IsPodRunning] Pod %s is in a terminal phase: %s", name, pod.Status.Phase)
		return false, nil
	}
	if pod.Status.Phase != corev1.PodRunning {
		log.Printf("[IsPodRunning] Pod %s is not yet Running, current phase: %s", name, pod.Status.Phase)
		return false, nil
	}

	if len(pod.Status.ContainerStatuses) == 0 {
		log.Printf("[IsPodRunning] Pod %s has no container statuses yet.", name)
		return false, nil
	}

	allContainersReady := true
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			allContainersReady = false
			log.Printf("[IsPodRunning] Container %s in pod %s is not ready. State: %+v", cs.Name, name, cs.State)
			if cs.State.Waiting != nil {
				log.Printf("[IsPodRunning] Container %s waiting reason: %s, message: %s", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
				if cs.State.Waiting.Reason == "CrashLoopBackOff" || cs.State.Waiting.Reason == "ImagePullBackOff" || cs.State.Waiting.Reason == "ErrImagePull" {
					return false, fmt.Errorf("container %s in CrashLoopBackOff or ImagePull error state", cs.Name)
				}
			}
			if cs.State.Terminated != nil {
				log.Printf("[IsPodRunning] Container %s terminated. Reason: %s, ExitCode: %d", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
				return false, fmt.Errorf("container %s terminated with exit code %d", cs.Name, cs.State.Terminated.ExitCode)
			}
			break
		}
	}

	if !allContainersReady {
		return false, nil
	}

	return true, nil
}

func (c *Client) ExecInPod(
	ctx context.Context,
	namespace, podName, containerName string,
	command []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	sizeQueue TerminalSizeQueue,
) error {
	log.Printf("[ExecInPod] Attempting to execute command in pod %s/%s, container %s: %v\n",
		namespace, podName, containerName, command)

	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	execOptions := &corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdin:     stdin != nil,
		Stdout:    stdout != nil,
		Stderr:    stderr != nil,
		TTY:       true,
	}

	req.VersionedParams(execOptions, scheme.ParameterCodec)

	log.Printf("[ExecInPod] Creating SPDY executor for URL: %s\n", req.URL().String())

	executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor for pod %s: %w", podName, err)
	}

	streamOptions := remotecommand.StreamOptions{
		Stdin:             stdin,
		Stdout:            stdout,
		Stderr:            stderr,
		Tty:               true,
		TerminalSizeQueue: &terminalSizeQueueAdapter{queue: sizeQueue},
	}

	log.Printf("[ExecInPod] Starting stream for pod %s...\n", podName)

	errChan := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ExecInPod PANIC] Recovered from panic during stream for pod %s: %v\n", podName, r)
				errChan <- fmt.Errorf("exec stream panicked for pod %s: %v", podName, r)
			}
		}()
		errChan <- executor.StreamWithContext(ctx, streamOptions)
	}()

	select {
	case err := <-errChan:
		if err != nil {
			log.Printf("[ExecInPod] Stream for pod %s completed with error: %v\n", podName, err)
			return fmt.Errorf("failed to execute command in pod %s: %w", podName, err)
		}
		log.Printf("[ExecInPod] Stream for pod %s completed successfully.\n", podName)
		return nil
	case <-ctx.Done():
		log.Printf("[ExecInPod] Context cancelled during exec for pod %s: %v\n", podName, ctx.Err())
		return fmt.Errorf("exec context cancelled for pod %s: %w", podName, ctx.Err())
	}
}

type terminalSizeQueueAdapter struct {
	queue TerminalSizeQueue
}

func (t *terminalSizeQueueAdapter) Next() *remotecommand.TerminalSize {
	size := t.queue.Next()
	if size == nil {
		return nil
	}
	return &remotecommand.TerminalSize{
		Width:  size.Width,
		Height: size.Height,
	}
}

// ServiceInfo represents information about a service running in a pod
type ServiceInfo struct {
	Name        string `json:"name"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Description string `json:"description"`
}

// GetServicesInPod discovers services running in the Kind cluster inside the DinD pod
func (c *Client) GetServicesInPod(ctx context.Context, podName, namespace string) ([]ServiceInfo, error) {
	var services []ServiceInfo
	
	// First, get services from the Kind cluster
	kindServices, err := c.GetKindClusterServices(ctx, podName, namespace)
	if err != nil {
		log.Printf("Failed to get Kind cluster services: %v", err)
	} else {
		services = append(services, kindServices...)
	}
	
	// Also check for services running directly in the DinD container
	dindServices, err := c.getDinDContainerServices(ctx, podName, namespace)
	if err != nil {
		log.Printf("Failed to get DinD container services: %v", err)
	} else {
		services = append(services, dindServices...)
	}
	
	return services, nil
}

// GetKindClusterServices gets services from the Kind cluster running inside DinD (Enhanced)
func (c *Client) GetKindClusterServices(ctx context.Context, podName, namespace string) ([]ServiceInfo, error) {
	// Create a shorter context for this operation
	execCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	
	// Enhanced service discovery with multiple methods (with timeouts)
	cmd := []string{"sh", "-c", `
		if command -v kubectl >/dev/null 2>&1; then
			# Check if cluster is ready with shorter timeout
			if timeout 3 kubectl cluster-info --request-timeout=3s >/dev/null 2>&1; then
				echo "=== KUBECTL_SERVICES ==="
				# Get all services with detailed information
				timeout 5 kubectl get services --all-namespaces --no-headers -o custom-columns="NAME:.metadata.name,NAMESPACE:.metadata.namespace,TYPE:.spec.type,CLUSTER-IP:.spec.clusterIP,EXTERNAL-IP:.status.loadBalancer.ingress[0].ip,PORTS:.spec.ports[*].port,TARGET-PORTS:.spec.ports[*].targetPort,PROTOCOLS:.spec.ports[*].protocol" --request-timeout=3s 2>/dev/null | grep -v '^kube-' | grep -v '^kubernetes ' || echo "no_user_services"
				
				echo "=== KUBECTL_ENDPOINTS ==="
				# Get endpoints to find actual running services (most important for verification)
				timeout 5 kubectl get endpoints --all-namespaces --no-headers -o custom-columns="NAME:.metadata.name,NAMESPACE:.metadata.namespace,ENDPOINTS:.subsets[*].addresses[*].ip,PORTS:.subsets[*].ports[*].port" --request-timeout=3s 2>/dev/null | grep -v '^kube-' | grep -v '^kubernetes ' || echo "no_endpoints"
			else
				echo "cluster_not_ready"
			fi
		else
			echo "kubectl_not_found"
		fi
	`}
	
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "dind",
			Command:   cmd,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr strings.Builder
	err = executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		log.Printf("Failed to execute kubectl in pod %s: %v, stderr: %s", podName, err, stderr.String())
		// If kubectl execution fails, try common port scanning as fallback
		return c.scanCommonPorts(ctx, podName, namespace)
	}

	output := stdout.String()
	if strings.Contains(output, "kubectl_not_found") || strings.Contains(output, "kubectl_failed") || strings.Contains(output, "cluster_not_ready") {
		// If kubectl is not available or cluster not ready, try common port scanning
		return c.scanCommonPorts(ctx, podName, namespace)
	}

	// Parse enhanced service discovery output
	services := c.parseEnhancedServiceOutput(output)
	
	// If no services found through Kubernetes API, try common port scanning as fallback
	if len(services) == 0 {
		log.Printf("No services found via kubectl in pod %s, trying port scanning fallback", podName)
		return c.scanCommonPorts(ctx, podName, namespace)
	}

	log.Printf("Found %d services via kubectl in pod %s", len(services), podName)
	return services, nil
}

// parseEnhancedServiceOutput parses the enhanced kubectl output for comprehensive service discovery
func (c *Client) parseEnhancedServiceOutput(output string) []ServiceInfo {
	var services []ServiceInfo
	serviceMap := make(map[string]*ServiceInfo) // Use map to deduplicate services

	// Parse services section
	if servicesStart := strings.Index(output, "=== KUBECTL_SERVICES ==="); servicesStart != -1 {
		servicesEnd := strings.Index(output[servicesStart:], "=== KUBECTL_INGRESSES ===")
		if servicesEnd == -1 {
			servicesEnd = len(output) - servicesStart
		}
		servicesSection := output[servicesStart:servicesStart+servicesEnd]
		
		if !strings.Contains(servicesSection, "no_user_services") {
			lines := strings.Split(strings.TrimSpace(servicesSection), "\n")
			for _, line := range lines[1:] { // Skip header line
				if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "===") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 6 {
					serviceName := parts[0]
					serviceNamespace := parts[1]
					serviceType := parts[2]
					
					// Parse ports (comma-separated)
					portsStr := parts[5]
					if portsStr != "<none>" && portsStr != "" {
						ports := strings.Split(portsStr, ",")
						for _, portStr := range ports {
							if port, err := strconv.Atoi(strings.TrimSpace(portStr)); err == nil {
								key := fmt.Sprintf("%s-%s-%d", serviceName, serviceNamespace, port)
								serviceMap[key] = &ServiceInfo{
									Name:        serviceName,
									Description: fmt.Sprintf("%s service in %s namespace (Type: %s)", serviceName, serviceNamespace, serviceType),
									Port:        port,
									Protocol:    "http", // Default to http, will be refined later
								}
							}
						}
					}
				}
			}
		}
	}

	// Parse ingresses section to identify HTTP services (but don't assume port 80 automatically)
	if ingressesStart := strings.Index(output, "=== KUBECTL_INGRESSES ==="); ingressesStart != -1 {
		ingressesEnd := strings.Index(output[ingressesStart:], "=== KUBECTL_ENDPOINTS ===")
		if ingressesEnd == -1 {
			ingressesEnd = len(output) - ingressesStart
		}
		ingressesSection := output[ingressesStart:ingressesStart+ingressesEnd]
		
		if !strings.Contains(ingressesSection, "no_ingresses") {
			lines := strings.Split(strings.TrimSpace(ingressesSection), "\n")
			for _, line := range lines[1:] { // Skip header line
				if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "===") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 5 {
					ingressName := parts[0]
					hostsStr := parts[2]
					backendService := parts[4]
					
					// Only add ingress if we can find the backend service in our service map
					found := false
					for key, service := range serviceMap {
						if strings.Contains(key, backendService) || strings.Contains(service.Name, backendService) {
							service.Description += fmt.Sprintf(" | Ingress: %s", hostsStr)
							found = true
							break
						}
					}
					
					// Don't create phantom services for ingresses without confirmed backends
					if !found {
						log.Printf("Ingress %s references backend %s but no corresponding service found", ingressName, backendService)
					}
				}
			}
		}
	}

	// Parse endpoints section to find actually running services
	if endpointsStart := strings.Index(output, "=== KUBECTL_ENDPOINTS ==="); endpointsStart != -1 {
		endpointsEnd := strings.Index(output[endpointsStart:], "=== KUBECTL_DEPLOYMENTS ===")
		if endpointsEnd == -1 {
			endpointsEnd = len(output) - endpointsStart
		}
		endpointsSection := output[endpointsStart:endpointsStart+endpointsEnd]
		
		if !strings.Contains(endpointsSection, "no_endpoints") {
			lines := strings.Split(strings.TrimSpace(endpointsSection), "\n")
			for _, line := range lines[1:] { // Skip header line
				if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "===") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 4 {
					endpointName := parts[0]
					endpointNamespace := parts[1]
					portsStr := parts[3]
					
					// Parse ports from endpoints
					if portsStr != "<none>" && portsStr != "" {
						ports := strings.Split(portsStr, ",")
						for _, portStr := range ports {
							if port, err := strconv.Atoi(strings.TrimSpace(portStr)); err == nil {
								key := fmt.Sprintf("%s-%s-%d", endpointName, endpointNamespace, port)
								if existing, exists := serviceMap[key]; exists {
									// Mark as verified (has endpoints)
									existing.Description += " ✓"
								} else {
									// Add new service discovered through endpoints
									serviceMap[key] = &ServiceInfo{
										Name:        endpointName + "-endpoint",
										Description: fmt.Sprintf("Endpoint: %s in %s namespace ✓", endpointName, endpointNamespace),
										Port:        port,
										Protocol:    "http",
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Parse deployments section to find potential services (but validate they have endpoints)
	if deploymentsStart := strings.Index(output, "=== KUBECTL_DEPLOYMENTS ==="); deploymentsStart != -1 {
		deploymentsSection := output[deploymentsStart:]
		
		if !strings.Contains(deploymentsSection, "no_deployments") {
			lines := strings.Split(strings.TrimSpace(deploymentsSection), "\n")
			for _, line := range lines[1:] { // Skip header line
				if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "===") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 6 {
					deploymentName := parts[0]
					deploymentNamespace := parts[1]
					readyReplicas := parts[2]
					availableReplicas := parts[3]
					containersStr := parts[4]
					portsStr := parts[5]
					
					// Only consider deployments with ready replicas and available replicas
					if readyReplicas != "0" && readyReplicas != "<none>" && 
					   availableReplicas != "0" && availableReplicas != "<none>" &&
					   readyReplicas == availableReplicas {
						// Parse container ports
						if portsStr != "<none>" && portsStr != "" {
							ports := strings.Split(portsStr, ",")
							for _, portStr := range ports {
								if port, err := strconv.Atoi(strings.TrimSpace(portStr)); err == nil {
									key := fmt.Sprintf("%s-%s-%d", deploymentName, deploymentNamespace, port)
									// Only add if not already present and if it's a common web port
									if _, exists := serviceMap[key]; !exists && isWebPort(port) {
										serviceMap[key] = &ServiceInfo{
											Name:        deploymentName + "-app",
											Description: fmt.Sprintf("App: %s (%s containers, %s ready) - Unverified", deploymentName, containersStr, readyReplicas),
											Port:        port,
											Protocol:    "http",
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Convert map to slice
	for _, service := range serviceMap {
		services = append(services, *service)
	}

	return services
}

// isWebPort checks if a port is commonly used for web services
func isWebPort(port int) bool {
	webPorts := map[int]bool{
		80:   true, // HTTP
		443:  true, // HTTPS
		3000: true, // Development servers
		8000: true, // HTTP alternative
		8080: true, // HTTP proxy/alternative
		8443: true, // HTTPS alternative
		9000: true, // Application servers
		5000: true, // Development servers
		4000: true, // Development servers
		3001: true, // Development servers
		8001: true, // HTTP alternative
		8888: true, // Jupyter/development
	}
	return webPorts[port]
}

// scanCommonPorts scans common ports to detect running services
func (c *Client) scanCommonPorts(ctx context.Context, podName, namespace string) ([]ServiceInfo, error) {
	commonPorts := []int{80, 443, 3000, 8000, 8080, 8443, 3001, 4000, 5000, 8001, 8888, 9000, 30000, 30001, 30002, 30003, 30080, 31000}
	
	var services []ServiceInfo
	
	// Check each common port
	for _, port := range commonPorts {
		cmd := []string{"sh", "-c", fmt.Sprintf(`
			# Try to connect to localhost:%d to see if something is listening
			timeout 1 bash -c "</dev/tcp/localhost/%d" >/dev/null 2>&1 && echo "port_%d_open" || echo "port_%d_closed"
		`, port, port, port, port)}
		
		req := c.clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(podName).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: "dind",
				Command:   cmd,
				Stdin:     false,
				Stdout:    true,
				Stderr:    true,
				TTY:       false,
			}, scheme.ParameterCodec)

		executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
		if err != nil {
			continue // Skip this port if execution fails
		}

		var stdout strings.Builder
		err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: &stdout,
		})

		if err != nil {
			continue // Skip this port if execution fails
		}

		output := stdout.String()
		if strings.Contains(output, fmt.Sprintf("port_%d_open", port)) {
			service := ServiceInfo{
				Name:        fmt.Sprintf("service-%d", port),
				Port:        port,
				Protocol:    "tcp",
				Description: getServiceDescription(port),
			}
			services = append(services, service)
		}
	}
	
	return services, nil
}

// getDinDContainerServices gets services running directly in the DinD container
func (c *Client) getDinDContainerServices(ctx context.Context, podName, namespace string) ([]ServiceInfo, error) {
	// Create a shorter context for this operation to avoid blocking
	execCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	
	// Execute netstat command to find listening ports in DinD container
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "dind",
			Command:   []string{"sh", "-c", "timeout 5 netstat -tlnp 2>/dev/null | grep LISTEN || timeout 5 ss -tlnp 2>/dev/null | grep LISTEN || echo 'No listening services found'"},
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr strings.Builder
	err = executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		log.Printf("Failed to execute netstat in pod %s: %v, stderr: %s", podName, err, stderr.String())
		// Return empty slice instead of error to avoid breaking the entire service discovery
		return []ServiceInfo{}, nil
	}

	output := stdout.String()
	if strings.Contains(output, "No listening services found") || output == "" {
		log.Printf("No listening services found via netstat in pod %s", podName)
		return []ServiceInfo{}, nil
	}

	services := parseNetstatOutput(output)
	log.Printf("Found %d services via netstat in pod %s", len(services), podName)
	return services, nil
}


// parseNetstatOutput parses netstat/ss output and returns service information
func parseNetstatOutput(output string) []ServiceInfo {
	var services []ServiceInfo
	lines := strings.Split(output, "\n")
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "LISTEN") {
			continue
		}
		
		// Parse different formats of netstat/ss output
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		
		var address string
		var protocol string
		
		// Detect if this is netstat or ss output
		if strings.HasPrefix(fields[0], "tcp") || strings.HasPrefix(fields[0], "udp") {
			// netstat format: tcp 0 0 0.0.0.0:80 0.0.0.0:* LISTEN
			protocol = fields[0]
			if len(fields) >= 4 {
				address = fields[3]
			}
		} else if len(fields) >= 2 && (strings.Contains(fields[1], ":") || strings.Contains(fields[0], ":")) {
			// ss format: LISTEN 0 128 *:80 *:*
			protocol = "tcp" // default assumption
			address = fields[1]
			if !strings.Contains(address, ":") && strings.Contains(fields[0], ":") {
				address = fields[0]
			}
		}
		
		if address == "" {
			continue
		}
		
		// Extract port from address (format could be 0.0.0.0:80, *:80, :::80, etc.)
		portStr := ""
		if strings.Contains(address, ":") {
			parts := strings.Split(address, ":")
			portStr = parts[len(parts)-1]
		}
		
		if portStr == "" || portStr == "*" {
			continue
		}
		
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		
		// Skip system ports and common internal services
		if port < 1024 && port != 80 && port != 443 && port != 8080 && port != 3000 && port != 8000 {
			continue
		}
		
		// Generate a description based on common port usage
		description := getServiceDescription(port)
		
		service := ServiceInfo{
			Name:        fmt.Sprintf("service-%d", port),
			Port:        port,
			Protocol:    protocol,
			Description: description,
		}
		
		// Check if this port is already in the list
		exists := false
		for _, existing := range services {
			if existing.Port == port {
				exists = true
				break
			}
		}
		
		if !exists {
			services = append(services, service)
		}
	}
	
	return services
}

// getServiceDescription returns a description for common port numbers
func getServiceDescription(port int) string {
	descriptions := map[int]string{
		80:   "HTTP Web Server",
		443:  "HTTPS Web Server",
		3000: "Development Server",
		8000: "HTTP Alternative",
		8080: "HTTP Proxy/Alternative",
		8443: "HTTPS Alternative",
		9000: "Application Server",
		3306: "MySQL Database",
		5432: "PostgreSQL Database",
		6379: "Redis Cache",
		27017: "MongoDB Database",
		5000: "Application Server",
		4000: "Application Server",
		8888: "Jupyter/Application Server",
		9090: "Prometheus/Monitoring",
		3001: "Development Server",
		8001: "HTTP Alternative",
		8002: "HTTP Alternative",
		8003: "HTTP Alternative",
		8004: "HTTP Alternative",
		8005: "HTTP Alternative",
	}
	
	if desc, exists := descriptions[port]; exists {
		return desc
	}
	
	return fmt.Sprintf("Service on port %d", port)
}




