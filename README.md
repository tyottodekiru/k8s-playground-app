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

The application consists of four main controllers:

1. **App Controller**: Web interface, OAuth authentication, and WebSocket terminal connections
2. **Generator Controller**: Creates new k8s environments (DinD pods) based on user requests
3. **Collector Controller**: Periodically checks for expired environments and marks them for cleanup
4. **Killer Controller**: Terminates environments that have been marked for shutdown

All controllers communicate through a Redis queue system, ensuring scalable and reliable operation.

## 🔧 Quick Start

### Prerequisites

- Kubernetes cluster (1.20+)
- `kubectl` configured to access your cluster
- `helm` 3.0+
- Google Cloud Platform project with OAuth 2.0 credentials

### Installation

1. **Add the Helm repository**
   ```bash
   helm repo add k8s-playground https://tyottodekiru.github.io/k8s-playground/
   helm repo update
   ```

2. **Configure Google OAuth Credentials**
   
   Create a values file (`my-values.yaml`):
   ```yaml
   auth:
     google:
       clientId: "your_google_client_id"
       clientSecret: "your_google_client_secret"
     sessionKey: "your_secure_session_key"
   
   baseURL: "https://k8s-playground.yourdomain.com"
   
   ingress:
     enabled: true
     hosts:
       - host: k8s-playground.yourdomain.com
         paths:
           - path: /
             pathType: Prefix
   ```

3. **Install K8s Playground**
   ```bash
   helm install my-k8s-playground k8s-playground/k8s-playground -f my-values.yaml
   ```

4. **Access the playground**
   
   Visit your configured domain or use port-forwarding for local access:
   ```bash
   kubectl port-forward svc/my-k8s-playground-app-controller 8080:80
   ```

See [docs/deployment.md](docs/deployment.md) for detailed deployment instructions and configuration options.

## 📚 Documentation

- [Deployment Guide](docs/deployment.md)
- [Configuration Reference](docs/configuration.md)
- [API Documentation](docs/api.md)
- [Contributing Guide](docs/contributing.md)

## 🔒 Security

- Secure Google OAuth 2.0 authentication
- Session-based user isolation
- RBAC-compliant Kubernetes permissions
- Automatic environment cleanup
- No persistent data storage in playgrounds

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
- Check the [documentation](docs/) for common questions
- Join our community discussions

## 🎯 Use Cases

- **Learning Kubernetes**: Safe environment to experiment with k8s concepts
- **Version Testing**: Test applications across different Kubernetes versions
- **Workshops & Training**: Provide isolated environments for participants
- **CI/CD Testing**: Temporary environments for integration testing
- **Development**: Quick k8s clusters for development and debugging

---

Made with ❤️ for the Kubernetes community