.PHONY: help dev dev-setup build deploy clean logs install-tools install-kind install-helm

# Default help command
help:
	@echo "Available commands:"
	@echo "  make dev          - Setup and run complete local development environment"
	@echo "  make port-forward - Port forward app to localhost:8080 (run after 'make dev')"
	@echo "  make install-tools - Install required development tools (kind, helm)"
	@echo "  make dev-setup    - Create kind cluster and setup environment"
	@echo "  make build        - Build all Go binaries"
	@echo "  make deploy       - Deploy/update application to kind cluster"
	@echo "  make build-images - Build all Docker images for development"
	@echo "  make clean        - Clean up development environment"

# Variables
CLUSTER_NAME = k8s-playground-dev
KIND_CONFIG = dev/kind-config.yaml
CHART_PATH = charts/k8s-playground-local
VALUES_FILE = dev/values.yaml
BIN_DIR = bin
TOOLS_DIR = .local/bin

# Controllers list
CONTROLLERS = app-controller generator-controller collector-controller killer-controller logging-controller

# Complete development setup
dev: install-tools dev-setup build build-images deploy
	@echo "🎉 Development environment ready!"
	@echo "🌐 To access your application, run: make port-forward"
	@echo "    Then visit: http://localhost:8080"

# Install development tools
install-tools: install-kind install-helm
	@echo "✅ All development tools installed"

# Install kind
install-kind:
	@echo "📥 Installing kind..."
	@mkdir -p $(TOOLS_DIR)
	@if command -v kind > /dev/null; then \
		echo "✅ kind is already installed"; \
	else \
		echo "  Downloading kind..."; \
		curl -Lo $(TOOLS_DIR)/kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64; \
		chmod +x $(TOOLS_DIR)/kind; \
		echo "✅ kind installed to $(TOOLS_DIR)/kind"; \
		echo "💡 Add $(PWD)/$(TOOLS_DIR) to your PATH or use ./$(TOOLS_DIR)/kind"; \
	fi

# Install helm
install-helm:
	@echo "📥 Installing helm..."
	@mkdir -p $(TOOLS_DIR)
	@if command -v helm > /dev/null; then \
		echo "✅ helm is already installed"; \
	else \
		echo "  Downloading helm..."; \
		curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3; \
		chmod 700 get_helm.sh; \
		HELM_INSTALL_DIR=$(PWD)/$(TOOLS_DIR) ./get_helm.sh --no-sudo; \
		rm get_helm.sh; \
		echo "✅ helm installed to $(TOOLS_DIR)/helm"; \
		echo "💡 Add $(PWD)/$(TOOLS_DIR) to your PATH or use ./$(TOOLS_DIR)/helm"; \
	fi

# Setup kind cluster
dev-setup:
	@echo "🚀 Setting up kind cluster..."
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	if ! command -v kind > /dev/null; then \
		echo "❌ kind is not found. Run 'make install-kind' first."; \
		exit 1; \
	fi; \
	if ! command -v helm > /dev/null; then \
		echo "❌ helm is not found. Run 'make install-helm' first."; \
		exit 1; \
	fi
	@mkdir -p $(BIN_DIR)
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	if kind get clusters | grep -q $(CLUSTER_NAME); then \
		echo "📋 Cluster $(CLUSTER_NAME) already exists"; \
	else \
		echo "🔧 Creating kind cluster $(CLUSTER_NAME)..."; \
		kind create cluster --name $(CLUSTER_NAME) --config $(KIND_CONFIG); \
	fi
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	echo "📦 Adding bitnami helm repository..."; \
	helm repo add bitnami https://charts.bitnami.com/bitnami || true; \
	helm repo update
	@echo "🐳 Preparing base images for development..."
	@if ! docker image inspect alpine:latest > /dev/null 2>&1; then \
		echo "  Pulling alpine:latest..."; \
		docker pull alpine:latest; \
	fi
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	echo "  Loading alpine image into kind cluster..."; \
	kind load docker-image alpine:latest --name $(CLUSTER_NAME)

# Build all Go binaries
build:
	@echo "🔨 Building Go binaries..."
	@mkdir -p $(BIN_DIR)
	@for controller in $(CONTROLLERS); do \
		echo "  Building $$controller..."; \
		CGO_ENABLED=0 GOOS=linux /usr/local/go/bin/go build -ldflags="-w -s" -o $(BIN_DIR)/$$controller ./cmd/$$controller; \
	done
	@echo "✅ All binaries built successfully"

# Build Docker images
build-images:
	@echo "🐳 Building Docker images..."
	@echo "  Building NFS Server development image..."
	@docker build -t k8s-playground/nfs-server:dev -f docker/nfs-server/Dockerfile.dev docker/nfs-server/
	@echo "✅ Docker images built successfully"

# Load images to kind cluster
load-images:
	@echo "📤 Loading images to kind cluster..."
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	kind load docker-image k8s-playground/nfs-server:dev --name $(CLUSTER_NAME)
	@echo "✅ Images loaded to kind cluster"

# Deploy to kind cluster
deploy: load-images
	@echo "🚀 Deploying to kind cluster..."
	@kubectl config use-context kind-$(CLUSTER_NAME)
	@echo "🔐 Creating authentication secret..."
	@kubectl create namespace k8s-playground --dry-run=client -o yaml | kubectl apply -f - || true
	@kubectl create secret generic k8s-playground-auth \
		--from-literal=sessionKey=dev-session-key-12345 \
		--from-literal=adminPassword=admin123 \
		--namespace k8s-playground \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "📦 Building chart dependencies..."
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	cd $(CHART_PATH) && helm dependency build
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	helm upgrade --install k8s-playground-local $(CHART_PATH) \
		--values $(VALUES_FILE) \
		--create-namespace \
		--namespace k8s-playground \
		--timeout 300s
	@echo "⏳ Waiting for deployments to be ready..."
	@kubectl wait --for=condition=available --timeout=300s deployment --all -n k8s-playground || true
	@echo "✅ Deployment completed!"
	@echo ""
	@echo "📊 Current status:"
	@kubectl get pods -n k8s-playground
	@echo ""
	@echo "🌐 Application should be available at: http://localhost:8080"

# Restart pods (useful after binary rebuild)
restart:
	@echo "🔄 Restarting all controllers..."
	@kubectl rollout restart deployment -n k8s-playground
	@echo "⏳ Waiting for rollout to complete..."
	@kubectl rollout status deployment/k8s-playground-app-controller -n k8s-playground
	@kubectl rollout status deployment/k8s-playground-generator-controller -n k8s-playground
	@kubectl rollout status deployment/k8s-playground-collector-controller -n k8s-playground
	@kubectl rollout status deployment/k8s-playground-killer-controller -n k8s-playground
	@kubectl rollout status statefulset/k8s-playground-logging-controller -n k8s-playground
	@kubectl rollout status statefulset/k8s-playground-nfs-server -n k8s-playground
	@echo "✅ All controllers restarted"

# Port forward to access application
port-forward:
	@echo "🌐 Port forwarding app-controller to localhost:8080..."
	@echo "   Visit http://localhost:8080 (Ctrl+C to stop)"
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	kubectl port-forward svc/k8s-playground-app-controller 8080:80 -n k8s-playground

# Quick rebuild and restart
rebuild: build restart
	@echo "🎯 Quick rebuild and restart completed!"

# Clean up development environment
clean:
	@echo "🧹 Cleaning up development environment..."
	@export PATH="$(PWD)/$(TOOLS_DIR):$$PATH"; \
	if kind get clusters | grep -q $(CLUSTER_NAME) 2>/dev/null; then \
		echo "🗑️  Deleting kind cluster $(CLUSTER_NAME)..."; \
		kind delete cluster --name $(CLUSTER_NAME); \
	else \
		echo "📋 Cluster $(CLUSTER_NAME) does not exist"; \
	fi
	@echo "🗑️  Cleaning build artifacts..."
	@rm -rf $(BIN_DIR)
	@docker rmi k8s-playground/nfs-server:dev 2>/dev/null || true
	@echo "✅ Cleanup completed"

# Show logs from all controllers
logs:
	@echo "📋 Showing logs from all controllers..."
	@kubectl config use-context kind-$(CLUSTER_NAME)
	@echo "=== App Controller ==="
	@kubectl logs -n k8s-playground deployment/k8s-playground-app-controller --tail=20 || echo "App controller not running"
	@echo ""
	@echo "=== Generator Controller ==="
	@kubectl logs -n k8s-playground deployment/k8s-playground-generator-controller --tail=20 || echo "Generator controller not running"
	@echo ""
	@echo "=== Collector Controller ==="
	@kubectl logs -n k8s-playground deployment/k8s-playground-collector-controller --tail=20 || echo "Collector controller not running"
	@echo ""
	@echo "=== Killer Controller ==="
	@kubectl logs -n k8s-playground deployment/k8s-playground-killer-controller --tail=20 || echo "Killer controller not running"
	@echo ""
	@echo "=== Logging Controller ==="
	@kubectl logs -n k8s-playground statefulset/k8s-playground-logging-controller --tail=20 || echo "Logging controller not running"

# Show status of all components
status:
	@echo "📊 Development environment status:"
	@kubectl config use-context kind-$(CLUSTER_NAME)
	@echo ""
	@echo "=== Pods ==="
	@kubectl get pods -n k8s-playground
	@echo ""
	@echo "=== Services ==="
	@kubectl get services -n k8s-playground
	@echo ""
	@echo "=== Port Forwarding Info ==="
	@echo "🌐 Access application: http://localhost:8080"

# Follow logs for a specific controller
logs-app:
	@kubectl logs -n k8s-playground deployment/k8s-playground-app-controller -f

logs-generator:
	@kubectl logs -n k8s-playground deployment/k8s-playground-generator-controller -f

logs-collector:
	@kubectl logs -n k8s-playground deployment/k8s-playground-collector-controller -f

logs-killer:
	@kubectl logs -n k8s-playground deployment/k8s-playground-killer-controller -f

logs-logging:
	@kubectl logs -n k8s-playground statefulset/k8s-playground-logging-controller -f
