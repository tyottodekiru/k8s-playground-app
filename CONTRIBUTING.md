# Contributing to k8s-playground

We welcome contributions to the k8s-playground project! This guide will help you set up a local development environment and contribute effectively.

## 🛠️ Local Development Environment Setup

### Prerequisites

The following tools must be installed on your system:

- [Docker](https://docs.docker.com/get-docker/) (20.10+) - **Required**
- [kubectl](https://kubernetes.io/docs/tasks/tools/) (1.20+) - **Required**  
- [Go](https://golang.org/doc/install) (1.24+) - **Required**

The following tools will be automatically installed by `make` commands:
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/) (0.11+) - **Auto-installed**
- [Helm](https://helm.sh/docs/intro/install/) (3.0+) - **Auto-installed**

### Go 1.24 Installation

This project uses Go 1.24. Install it using the following steps:

```bash
# Download and install Go 1.24
wget https://go.dev/dl/go1.24.3.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.3.linux-amd64.tar.gz

# Add to PATH (add to ~/.bashrc or ~/.zshrc)
export PATH=$PATH:/usr/local/go/bin

# Apply settings
source ~/.bashrc  # or source ~/.zshrc

# Verify installation
go version  # should show: go version go1.24.3 linux/amd64
```

**Note**: If you have an older version of Go installed, make sure to update to 1.24.

### kubectl Installation

kubectl is required for interacting with Kubernetes clusters. Install it using the following steps:

```bash
# Download the latest kubectl binary
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"

# Make it executable
chmod +x kubectl

# Move to a directory in your PATH
sudo mv kubectl /usr/local/bin/

# Verify installation
kubectl version --client  # should show client version
```

**Alternative installation methods:**
- **Ubuntu/Debian**: `sudo apt-get install kubectl`
- **CentOS/RHEL**: `sudo yum install kubectl`
- **macOS**: `brew install kubectl`

### Docker Installation

Docker is required for running containers and kind clusters. If Docker is not installed:

```bash
# Ubuntu/Debian
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# Verify installation
docker --version  # should show Docker version
```

**Alternative installation methods:**
- **Ubuntu/Debian**: `sudo apt-get install docker.io`
- **CentOS/RHEL**: `sudo yum install docker`
- **macOS**: Install [Docker Desktop](https://docs.docker.com/desktop/mac/)
- **Windows**: Install [Docker Desktop](https://docs.docker.com/desktop/windows/)

### Docker Permissions Setup

To use Docker as a non-root user, run the following commands:

```bash
# Add current user to docker group
sudo usermod -aG docker $USER

# Apply the setting (choose one of the following)
newgrp docker  # Start new shell session
# or
# Log out and log back in
# or
# Open a new terminal
```

**Important**: After running the usermod command, you **must start a new shell session**. The setting will not be applied in the current session.

This setup is only required **once**.

### Installation Verification

Before proceeding, verify that all required tools are properly installed:

```bash
# Verify all required tools are installed and accessible
go version        # Should show: go version go1.24.3 linux/amd64
docker --version  # Should show: Docker version 20.10.x
kubectl version --client  # Should show kubectl client version
docker ps         # Should work without permission errors

# All commands above should succeed without errors
```

If any command fails, please review the installation steps above.

## 🚀 Quick Start

### 1. Initial Setup

```bash
# Complete development environment setup (first time only)
make dev
```

This command will sequentially execute:
- Create kind cluster
- Build Go binaries
- Build Docker images
- Deploy with Helm

The setup automatically configures password authentication with default credentials (admin/admin123).

### 2. Access the Application

After the initial setup is complete, start port forwarding to access the application:

```bash
# Start port forwarding (run this in a separate terminal)
make port-forward
```

Then visit:

🌐 **http://localhost:8080**

### 3. Login Credentials for Local Development

The local development environment is configured with password authentication mode. Use the following credentials to log in:

- **Username**: `admin`
- **Password**: `admin123`

**Security Note**: These are development-only credentials and should **NEVER** be used in production environments. The credentials are automatically configured by the `make dev` command and are defined in:
- `Makefile:121-122`: Creates the k8s-playground-auth secret with these credentials
- `dev/values.yaml`: Sets AUTH_METHOD to "password" for local development

**Important**: In production, use Google OAuth authentication or change the default password immediately.

## 🔄 Development Workflow

### Applying Changes After Source Code Modifications

```bash
# After changing Go code
make rebuild

# Or manually run
make build    # Rebuild binaries
make restart  # Restart pods
```

### Individual Commands

```bash
# Show help
make help

# Install development tools (kind, helm) only
make install-tools

# Setup kind cluster only
make dev-setup

# Build binaries only
make build

# Deploy/update only
make deploy

# Clean up environment
make clean

# View logs
make logs

# Follow specific controller logs
make logs-app         # App Controller
make logs-generator   # Generator Controller
make logs-collector   # Collector Controller
make logs-killer      # Killer Controller
make logs-logging     # Logging Controller

# Check environment status
make status
```

## 🏗️ Architecture

### Local Development Environment Structure

```
Host Machine (localhost:8080)
  ↓
kind cluster (k8s-playground-dev)
  ├── App Controller (NodePort 30080)
  ├── Generator Controller
  ├── Collector Controller
  ├── Killer Controller
  ├── Logging Controller
  ├── Redis (Bitnami chart)
  └── NFS Server
```

### File Structure

```
.
├── dev/
│   ├── kind-config.yaml      # kind cluster config (with port mapping)
│   └── values.yaml           # Local development Helm values
├── charts/k8s-playground-local/  # Local development Helm chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
├── bin/                      # Built Go binaries (gitignored)
├── Makefile                  # Development commands
├── CONTRIBUTING.md           # This file
└── README-dev.md            # Legacy development docs
```

## ⚡ Fast Development Tips

### 1. Binary Mount Approach

- No Docker image rebuild required
- Local Go compilation → Direct mount to kind cluster
- Change reflection time: seconds

### 2. Efficient Development Cycle

```bash
# 1. Edit source code
vim cmd/app-controller/main.go

# 2. Fast rebuild & restart
make rebuild

# 3. Check in browser
open http://localhost:8080
```

### 3. Debugging Commands

```bash
# Check overall status
make status

# Monitor specific controller logs
make logs-app

# Pod detailed information
kubectl describe pod -n k8s-playground

# List all cluster resources
kubectl get all -n k8s-playground
```

## 🔧 Troubleshooting

### Common Issues and Solutions

1. **kind cluster won't start**
   ```bash
   make clean  # Clean up environment
   make dev    # Setup again
   ```

2. **Port 8080 is in use**
   ```bash
   # Check processes using the port
   lsof -i :8080
   # Stop the process if necessary
   ```

3. **Binaries not updating**
   ```bash
   # Check bin directory permissions
   ls -la bin/
   # Manual build
   make build
   ```

4. **Pods not starting**
   ```bash
   # Check pod status
   kubectl get pods -n k8s-playground
   # Check logs
   kubectl logs -n k8s-playground deployment/k8s-playground-local-app-controller
   ```

### Log Viewing Methods

```bash
# All controller logs
make logs

# Real-time specific controller logs
make logs-app

# Direct kubectl usage
kubectl logs -n k8s-playground deployment/k8s-playground-local-app-controller -f
```

## 🧹 Environment Cleanup

To completely clean up the development environment:

```bash
make clean
```

This command will:
- Delete the kind cluster
- Remove build artifacts
- Remove Docker images

## 🔄 Development vs Production Environment

| Item | Local Development | Production |
|------|-------------------|------------|
| Kubernetes | kind cluster | Real K8s cluster |
| Images | Alpine-based + binary mount | Full Docker images |
| Data persistence | None (development) | PVC etc. for persistence |
| Access method | localhost:8080 | Actual domain |
| TLS | None | Yes |

## 📝 Contribution Guidelines

### Code Standards

- Follow existing Go code conventions
- Use meaningful variable and function names
- Add comments for complex logic
- Ensure all tests pass

### Pull Request Process

1. **Fork the repository**
2. **Set up local development environment**
   ```bash
   git clone https://github.com/YOUR_USERNAME/k8s-playground.git
   cd k8s-playground
   make dev
   ```

3. **Create a feature branch**
   ```bash
   git checkout -b feature/your-feature-name
   ```

4. **Make your changes and test**
   ```bash
   # Make changes
   make rebuild  # Test your changes
   ```

5. **Run tests (if applicable)**
   ```bash
   go test ./...
   ```

6. **Commit and push**
   ```bash
   git add .
   git commit -m "feat: add your feature description"
   git push origin feature/your-feature-name
   ```

7. **Create a Pull Request**
   - Describe your changes clearly
   - Reference any related issues
   - Include screenshots if UI changes are involved

### Testing Your Changes

Before submitting a PR, make sure to:

1. **Test locally**
   ```bash
   make dev          # Full setup
   make rebuild      # After changes
   ```

2. **Access the application**
   - Visit http://localhost:8080
   - Login with: admin / admin123
   - Test the functionality you changed
   - Check logs for errors: `make logs`

3. **Clean up**
   ```bash
   make clean        # Clean environment
   ```

## 🆘 Getting Help

If you encounter issues during development:

1. **Check this guide** for common solutions
2. **Search existing issues** on GitHub
3. **Create a new issue** with:
   - Steps to reproduce the problem
   - Your environment details (OS, Go version, etc.)
   - Error messages and logs

## 📚 Development Chart Documentation

For detailed information about the local development Helm chart and how it differs from the public chart, see:

**English**: [charts/k8s-playground-local/DEVELOPMENT.md](charts/k8s-playground-local/DEVELOPMENT.md)  
**Japanese**: [charts/k8s-playground-local/DEVELOPMENT.ja.md](charts/k8s-playground-local/DEVELOPMENT.ja.md)

This documentation explains:
- Binary mounting for hot reload development
- NFS server local development adaptations  
- Resource optimizations for local environments
- Implementation rationale and background for each modification
- Differences from the public k8s-playground-chart

## 🎉 Recognition

Contributors will be recognized in the project README and release notes. Thank you for helping make k8s-playground better!

---

When developing, use `make dev` for initial setup and `make rebuild` to apply changes as you develop.