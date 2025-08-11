# Local Development Chart - Differences from Public Chart

This directory (`charts/k8s-playground-local/`) contains a Helm chart specifically designed for local development.
While based on the [public k8s-playground-chart](https://github.com/tyottodekiru/k8s-playground-chart), it includes several critical modifications for optimal local development experience.

## 🎯 Development Challenges and Solutions

### Traditional Development Flow Problems
1. **Image rebuild required** for every code change, resulting in slow development cycles
2. **Multiple controllers** (app, generator, collector, killer, logging) difficult to develop simultaneously
3. **NFS server setup** and user environment configuration complexity
4. **Authentication setup** (Google OAuth) complicates local testing

### Solution Strategy
- **Binary mounting** for hot-reload development environment
- **Lightweight containers + local builds** for fast development cycles
- **Zero application implementation changes** constraint compliance
- **Minimal divergence** from public chart design

## 🔧 Key Modifications

### 1. Binary Mounting (Hot Reload)
**Modified Files**: All controller Deployment/StatefulSet templates

**Background**: 
Traditional development required a 5-10 minute cycle of code change → image build → load → deploy.
We needed to reduce this to just Go compilation (10-30 seconds).

**Implementation Rationale**:
- **hostPath volumes**: Share host's `/mnt/bin` directory within kind cluster
- **Lightweight Alpine**: Provide minimal execution environment without application binaries
- **Runtime mounting**: Use latest locally-built binaries at container startup

**Constraint Compliance**:
Adjust binary paths for environment compatibility without modifying application implementation.

```yaml
# Example: app-controller
command: ["/mnt/bin/app-controller"]
volumeMounts:
  - name: binary-volume
    mountPath: /mnt/bin
volumes:
  - name: binary-volume
    hostPath:
      path: /mnt/bin
      type: Directory
```

### 2. Web Assets Mounting
**Modified Files**: app-controller template

**Background**: 
HTML templates, CSS, JavaScript, and other frontend assets are frequently modified
and require hot-reload capability similar to binaries.

**Implementation Rationale**:
- **hostPath mounting**: Direct mount from `/mnt/web` to `/app/web`
- **Template path correction**: Relative paths changed to absolute in `internal/controllers/app.go`

### 3. NFS Server Local Development Support
**Modified Files**: `templates/nfs-server-statefulset.yaml`

**Background**: 
Application code hardcodes `/exports` paths, and full persistence functionality
is unnecessary for development. However, application implementation cannot be modified.

**Implementation Rationale**:
- **tmpfs usage**: Fast memory-based operation with clean state on Pod restart
- **symlink strategy**: `/exports` → `/exports-tmpfs` for existing code compatibility
- **lifecycle hooks**: Automatic symlink creation at Pod startup to reduce operational overhead

**Technical Challenge Resolution**:
- When NFS server uses tmpfs, existing `/exports` directory conflicts
- `rm -rf /exports && ln -sf /exports-tmpfs /exports` provides atomic replacement

```yaml
# Local development additional configuration
env:
- name: SHARED_DIRECTORY
  value: "/exports-tmpfs"  # Use tmpfs
lifecycle:
  postStart:
    exec:
      command: ["/bin/sh", "-c", "rm -rf /exports && ln -sf /exports-tmpfs /exports || true"]
```

### 4. Authentication Method Change
**Modified Files**: `dev/values.yaml`

**Background**: 
Google OAuth requires callback URL configuration and other setup that creates
development barriers. Developers want to focus on feature development.

**Implementation Rationale**:
- **Switch to password auth**: Application already supports this method
- **Fixed credentials**: `admin` / `admin123` for immediate login
- **Security consideration**: Local development environment only

### 5. Resource Limit Adjustments
**Modified Files**: All controller resource configurations

**Background**: 
Developer laptop environments don't need production-level resources.
Lightweight configuration enables coexistence with multiple development projects.

**Implementation Rationale**:
- **Significant resource reduction**: memory 128Mi～512Mi, CPU 100m～500m
- **Development experience priority**: Minimal resources for functional verification

```yaml
resources:
  requests:
    memory: "128Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"  # Greatly reduced from public version
    cpu: "500m"
```

### 6. Image Pull Policy
**Modified Files**: All controllers

**Background**: 
In kind cluster environments, we want to use locally-built and loaded images
rather than pulling latest from registries.

**Implementation Rationale**:
- **`pullPolicy: "Never"`**: Completely disable registry access
- **Development acceleration**: Immediate container startup without network communication
- **Offline development**: Enables development without internet connectivity

### 7. Namespace Unification
**Modified Files**: `dev/values.yaml`

**Background**: 
Development environments benefit from consolidating all resources in a single
namespace for simplified management and cleanup. Public charts target
production multi-tenant operations.

**Implementation Rationale**:
- **`k8s-playground` namespace**: Dedicated development environment isolation
- **Simplified resource management**: Complete cleanup with `kubectl delete namespace k8s-playground`
- **Interference avoidance**: Separation from other development projects

## 📂 File Structure Differences

```
charts/k8s-playground-local/
├── Chart.yaml                    # Identical to public chart
├── values.yaml                   # Identical to public chart (default values)
├── templates/                    # Following files are modified
│   ├── deployment-app-controller.yaml      # Binary mounting support
│   ├── deployment-collector-controller.yaml
│   ├── deployment-generator-controller.yaml
│   ├── deployment-killer-controller.yaml
│   ├── deployment-logging-controller.yaml
│   ├── nfs-server-statefulset.yaml        # tmpfs + symlink support
│   └── service.yaml                       # port-forward support
├── DEVELOPMENT.md                # This file (English)
└── DEVELOPMENT.ja.md            # Japanese version
```

## 🚀 Usage

1. **Development Environment Setup**:
   ```bash
   make dev
   ```

2. **Application Access**:
   ```bash
   make port-forward
   # Access http://localhost:8080 in browser
   # Login: admin / admin123
   ```

3. **Applying Code Changes**:
   ```bash
   make build    # Only rebuild Go binaries
   # No container restart or image rebuild required
   ```

## ⚠️ Important Notes

1. **DO NOT use in production**: This chart is development-only
2. **Security considerations**: Uses fixed passwords and privileged containers
3. **Data persistence**: NFS uses tmpfs, data is lost on Pod restart
4. **Application implementation is immutable**: Only Helm chart level modifications, no application code changes

## 🔄 Synchronization with Public Chart

When the public chart is updated:
1. Sync `values.yaml` and `Chart.yaml` with public version
2. Sync `templates/` files while preserving the above modifications
3. Add similar development support for new features

---

This local development chart enables efficient k8s-playground development for contributors.

## 📖 Documentation

- **English**: [DEVELOPMENT.md](DEVELOPMENT.md) (this file)
- **日本語**: [DEVELOPMENT.ja.md](DEVELOPMENT.ja.md)