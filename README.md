# K8s Playground

A web-based Kubernetes playground that allows users to easily create and manage temporary Kubernetes environments using Docker-in-Docker (DinD) technology. Perfect for learning, testing, and experimenting with different Kubernetes versions.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 🚀 Features

- **Multi-version Kubernetes Support**: Create environments with different Kubernetes versions (1.30-1.33)
- **Google OAuth 2.0 Authentication**: Secure login using Google accounts
- **User-Specific Environments**: Each user manages their own isolated set of k8s playgrounds
- **Browser-based Terminal**: Interactive shell interface using xterm.js for cluster interaction
- **Auto-cleanup**: Automatic environment cleanup after 24 hours
- **Internal Browser Support**: Access services within the DinD k8s cluster
- **Scalable Architecture**: Microservices-based design with Redis queue system

## 🏗️ Architecture

The application consists of five main controllers:

1. **App Controller**: Web interface, OAuth authentication, and WebSocket terminal connections
2. **Generator Controller**: Creates new k8s environments (DinD pods) based on user requests
3. **Collector Controller**: Periodically checks for expired environments and marks them for cleanup
4. **Killer Controller**: Terminates environments that have been marked for shutdown
5. **Logging Controller**: Handles application logging and monitoring with persistent storage

### Infrastructure Components

- **Redis**: Message queue system for inter-controller communication
- **NFS Server**: Shared storage for DinD environments

All controllers communicate through a Redis queue system, ensuring scalable and reliable operation.

## 🔧 Quick Start

### Prerequisites

- Kubernetes cluster (1.20+)
- `kubectl` configured to access your cluster
- `helm` 3.0+
- Google Cloud Platform project with OAuth 2.0 credentials

### Installation

#### Option 1: Using Helm Repository (Recommended)

1. **Add the Helm repository**
   ```bash
   helm repo add k8s-playground https://k8s-playground.com
   helm repo update
   ```

2. **Create a values file** (`values.yaml`) for password authentication:
   ```yaml
   global:
     baseURL: "https://example.com"
   
   controlPlane:
     authentication:
       method: "password"
       secretName: "k8s-playground-auth"
   ```

3. **Create the authentication secret**
   ```bash
   # Create secret with admin password
   kubectl create secret generic k8s-playground-auth \
     --from-literal=sessionKey=your_secure_session_key \
     --from-literal=adminPassword=your_password
   
   # Or create from YAML
   kubectl apply -f - <<EOF
   apiVersion: v1
   kind: Secret
   metadata:
     name: k8s-playground-auth
   type: Opaque
   data:
     sessionKey: xxxxxxxxx      # base64 encoded "your_secure_session_key"
     adminPassword: xxxxxxxxx=  # base64 encoded "your_password"
   EOF
   ```

4. **Install with Helm**
   ```bash
   helm install k8s-playground k8s-playground/k8s-playground --values values.yaml
   ```

#### Option 2: Google OAuth Authentication

For Google OAuth setup, create a values file with Google credentials:

```yaml
global:
  baseURL: "https://k8s-playground.yourdomain.com"

controlPlane:
  authentication:
    method: "google"
    google:
      clientId: "your_google_client_id"
      allowedDomains: ["yourdomain.com"]
      adminUsers: ["admin@yourdomain.com"]
```

Then create the secret and install:
```bash
# Create authentication secret
kubectl create secret generic k8s-playground-auth \
  --from-literal=clientSecret=your_google_client_secret \
  --from-literal=sessionKey=your_secure_session_key 

# Install the chart
helm install k8s-playground k8s-playground/k8s-playground --values values.yaml
```

#### Option 3: From Source

```bash
git clone https://github.com/tyottodekiru/k8s-playground-chart.git
cd k8s-playground-chart
helm install k8s-playground ./charts/k8s-playground --values values.yaml
```

### Access the Application

Visit your configured domain or use port-forwarding for local access:
```bash
kubectl port-forward svc/k8s-playground-app-controller 8080:80
```

Then open your browser to `http://localhost:8080`

- **Password Authentication**: Use the admin password you configured in the secret
- **Google OAuth**: Login with your Google account

### Admin Panel Access

You can access the admin panel by adding `/admin` to your base URL:
- Local: `http://localhost:8080/admin`
- Production: `https://your-domain.com/admin`

The admin panel allows you to:
- Monitor active playground environments
- View command execution history
- Manage user sessions and environments

⚠️ **Security Note for Password Authentication**: When using password authentication mode (intended for development purposes), all users have access to the admin panel and can view command execution history from all users. For production use, consider using Google OAuth authentication which provides proper user isolation.

## 🔒 Security

- Secure Google OAuth 2.0 authentication
- Session-based user isolation
- Automatic environment cleanup

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](docs/contributing.md) for details.

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🆘 Support

- Create an issue for bug reports or feature requests
- Join our community discussions

## 🎯 Use Cases

- **Learning Kubernetes**: Safe environment to experiment with k8s concepts
- **Version Testing**: Test applications across different Kubernetes versions
- **Workshops & Training**: Provide isolated environments for participants
- **CI/CD Testing**: Temporary environments for integration testing
- **Development**: Quick k8s clusters for development and debugging

