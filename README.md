> [!WARNING]
> This repository is under active development and not ready for production use. APIs and concepts may change without notice, including breaking changes.

# platform-mesh-operator

A Kubernetes operator that bootstraps and manages platform-mesh environments by reconciling `PlatformMesh` custom resources.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [PlatformMesh Resource](#platformmesh-resource)
  - [Exposure](#exposure)
  - [KCP Configuration](#kcp-configuration)
  - [OCM Configuration](#ocm-configuration)
  - [Values](#values)
  - [Profile Configuration](#profile-configuration)
  - [Feature Toggles](#feature-toggles)
- [Operator Configuration](#operator-configuration)
- [Subroutines](#subroutines)
- [Development](#development)
- [Contributing](#contributing)

## Overview

The platform-mesh-operator reconciles `PlatformMesh` resources to:

- Deploy infrastructure and runtime components via Helm releases
- Configure KCP workspaces, API bindings, and provider connections
- Manage secrets for cluster connectivity
- Apply feature toggles and custom configurations

## Quick Start

1. Install the operator (typically via Helm chart)
2. Create a `PlatformMesh` resource:

```yaml
apiVersion: core.platform-mesh.io/v1alpha1
kind: PlatformMesh
metadata:
  name: platform-mesh
  namespace: platform-mesh-system
spec:
  exposure:
    baseDomain: example.com
    port: 443
    protocol: https
  ocm:
    repo:
      name: platform-mesh
    component:
      name: platform-mesh
    referencePath:
    - name: core
```

## PlatformMesh Resource

### Exposure

Configures how services are exposed externally:

```yaml
spec:
  exposure:
    baseDomain: example.com  # Base domain for all services
    port: 443                # External port
    protocol: https          # http or https
```

### KCP Configuration

Manages KCP workspace setup and cluster connections.

#### Provider Connections

Connect platform-mesh to provider Kubernetes clusters:

```yaml
spec:
  kcp:
    providerConnections:
    - endpointSliceName: core.platform-mesh.io
      path: root:platform-mesh-system
      secret: provider-kubeconfig
      external: false  # Optional, defaults to false

    extraProviderConnections:  # Additional providers
    - endpointSliceName: auxiliary.platform-mesh.io
      path: root:auxiliary-system
      secret: auxiliary-kubeconfig
```

#### Extra Workspaces and API Bindings

```yaml
spec:
  kcp:
    extraWorkspaces:
    - path: root:orgs:my-workspace
      type:
        name: universal
        path: root

    extraDefaultAPIBindings:
    - workspaceTypePath: root:types
      export: services
      path: root:exports
```

### OCM Configuration

Configure Open Component Model integration:

```yaml
spec:
  ocm:
    repo:
      name: platform-mesh       # Repository name
    component:
      name: platform-mesh       # Component name
    referencePath:              # Reference path to follow
    - name: core
    - name: services
```

### Values

Custom values passed directly to deployed Helm charts:

```yaml
spec:
  values:
    service1:
      enabled: true
      targetNamespace: default
      values:
        type: None
    service2:
      enabled: false
```

### Profile Configuration

The deployment profile controls infrastructure and component configuration. Stored in a ConfigMap with two sections: `infra` and `components`.

#### Default Profile

If no custom profile is specified, the operator creates `<platform-mesh-name>-profile` automatically.

#### Custom Profile

Reference a custom ConfigMap:

```yaml
spec:
  profileConfigMap:
    name: my-custom-profile
    namespace: platform-mesh-system  # Optional
```

The ConfigMap must contain `profile.yaml`:

```yaml
infra:
  ocm:
    skipVerify: true
    interval: 3m
  gatewayApi:
    enabled: true
    name: gateway-api
  traefik:
    enabled: true
    targetNamespace: default

components:
  targetNamespace: platform-mesh-system
  protocol: https
  port: 443
  services:
    account-operator:
      enabled: true
      values: {}
```

### Feature Toggles

Enable or disable specific features:

```yaml
spec:
  featureToggles:
  - name: feature-enable-getting-started
  - name: feature-enable-marketplace-account
```

| Toggle | Description |
|--------|-------------|
| `feature-enable-getting-started` | Getting Started UI page |
| `feature-enable-marketplace-account` | Marketplace at account level |
| `feature-enable-marketplace-org` | Marketplace at organization level |
| `feature-accounts-in-accounts` | Display accounts within account context |
| `feature-enable-account-iam-ui` | IAM UI Members section at account level |
| `feature-disable-email-verification` | Disable email verification |
| `feature-disable-contentconfigurations` | Disable ContentConfiguration loading |

## Operator Configuration

Configure via environment variables or CLI flags (kebab-case, e.g., `--kcp-url`).

### General

| Variable | Description | Default |
|----------|-------------|---------|
| `KUBECONFIG` | Kubeconfig path for PlatformMesh reconciliation | In-cluster |
| `WORKSPACE_DIR` | Working directory for operator files | `/operator/` |
| `PATCH_OIDC_CONTROLLER_ENABLED` | Enable OIDC controller patching | `false` |
| `LEADER_ELECTION_ID` | Leader election identifier | `81924e50.platform-mesh.org` |

### KCP

| Variable | Description | Default |
|----------|-------------|---------|
| `KCP_URL` | KCP API server URL | (required) |
| `KCP_NAMESPACE` | KCP components namespace | `platform-mesh-system` |
| `KCP_ROOT_SHARD_NAME` | KCP root shard name | `root` |
| `KCP_FRONT_PROXY_NAME` | KCP front proxy name | `frontproxy` |
| `KCP_FRONT_PROXY_PORT` | KCP front proxy port | `6443` |
| `KCP_CLUSTER_ADMIN_SECRET_NAME` | KCP admin cert secret | `kcp-cluster-admin-client-cert` |

### Subroutines

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_DEPLOYMENT_ENABLED` | Enable deployment subroutine | `true` |
| `SUBROUTINES_DEPLOYMENT_ENABLE_ISTIO` | Enable Istio integration | `true` |
| `SUBROUTINES_KCP_SETUP_ENABLED` | Enable KCP setup subroutine | `true` |
| `SUBROUTINES_PROVIDER_SECRET_ENABLED` | Enable provider secret subroutine | `true` |
| `SUBROUTINES_FEATURE_TOGGLES_ENABLED` | Enable feature toggles subroutine | `false` |
| `SUBROUTINES_RESOURCE_ENABLED` | Enable resource subroutine | `true` |
| `SUBROUTINES_WAIT_ENABLED` | Enable wait subroutine | `true` |

### OIDC Patching

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_PATCH_OIDC_CONFIGMAP_NAME` | OIDC ConfigMap name | `oidc-authentication-config` |
| `SUBROUTINES_PATCH_OIDC_NAMESPACE` | OIDC namespace | `platform-mesh-system` |
| `SUBROUTINES_PATCH_OIDC_BASEDOMAIN` | OIDC base domain | `portal.dev.local:8443` |
| `SUBROUTINES_PATCH_OIDC_DOMAIN_CA_LOOKUP` | Enable domain CA lookup | `false` |

### Authorization Webhook

| Variable | Description | Default |
|----------|-------------|---------|
| `AUTHORIZATION_WEBHOOK_SECRET_NAME` | Webhook secret name | `kcp-webhook-secret` |
| `AUTHORIZATION_WEBHOOK_SECRET_CA_NAME` | Webhook CA certificate name | `rebac-authz-webhook-cert` |

### Remote Cluster Configuration

#### Remote Infrastructure

| Variable | Description | Default |
|----------|-------------|---------|
| `REMOTE_INFRA_ENABLED` | Enable remote infra reconciliation | `false` |
| `REMOTE_INFRA_KUBECONFIG` | Remote infra kubeconfig path | `/operator/infra-kubeconfig` |

#### Remote Runtime

| Variable | Description | Default |
|----------|-------------|---------|
| `REMOTE_RUNTIME_ENABLED` | Enable remote runtime reconciliation | `false` |
| `REMOTE_RUNTIME_KUBECONFIG` | Remote runtime kubeconfig path | `/operator/runtime-kubeconfig` |
| `REMOTE_RUNTIME_INFRA_SECRET_NAME` | Infra kubeconfig secret name | `infra-kubeconfig` |
| `REMOTE_RUNTIME_INFRA_SECRET_KEY` | Infra kubeconfig secret key | `kubeconfig` |

## Subroutine Details

The operator processes `PlatformMesh` resources through these subroutines:

### Deployment

Manages component deployment across clusters:

- Loads/creates deployment profiles from ConfigMap
- Merges custom values with profile configurations
- Applies templated manifests (`Resource`, `HelmRelease`)
- Configures OCM integration
- Manages authorization webhook secrets (issuer, certificate, KCP webhook)
- Waits for dependencies: HelmReleases, cert-manager, Istio, KCP components
- Triggers operator restart if Istio sidecar is missing

**Value templating**: HelmReleases receive `spec.values` from `PlatformMesh.Spec.Values` with these templated fields:

- `baseDomain`, `baseDomainPort`, `port`, `protocol`, `iamWebhookCA`

### KcpSetup

Initializes the KCP environment:

- Creates workspaces from `providerConnections`
- Sets up API bindings from `extraDefaultAPIBindings`
- Creates extra workspaces from `spec.kcp.extraWorkspaces`

### ProviderSecret

Manages provider connection secrets:

- Creates/updates secrets for `providerConnections` and `extraProviderConnections`
- Maintains access credentials for provider clusters

### Defaults

Applies default configurations:

- Sets defaults for `ocm.repo.name` and `ocm.component.name`
- Ensures consistent baseline configuration

### Webhook

Manages webhook configurations:

- Sets up validation/mutation webhooks
- Configures webhook secrets

### Wait

Ensures resources are ready before proceeding:

- Waits for resources to match conditions (e.g., HelmRelease Ready)
- Supports filtering by namespace, labels, API versions
- Requeues reconciliation if resources aren't ready

Custom wait configuration:

```yaml
spec:
  wait:
    resourceTypes:
    - apiVersions:
        versions: ["v2"]
      groupKind:
        group: helm.toolkit.fluxcd.io
        kind: HelmRelease
      namespace: default
      labelSelector:
        matchExpressions:
        - key: helm.toolkit.fluxcd.io/name
          operator: In
          values: ["my-release"]
      conditionStatus: "True"
      conditionType: Ready
```

## Development

### Requirements

- Go (see [go.mod](go.mod) for version)
- Access to a Kubernetes cluster

### Running Tests

```bash
task test
# or
go test ./...
```

### Linting

```bash
task lint
# or
golangci-lint run
```

## Releasing

Releases are automated via GitHub Actions. All versions are available as Go modules through GitHub.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## Code of Conduct

Please refer to our [Code of Conduct](https://github.com/platform-mesh/.github/blob/main/CODE_OF_CONDUCT.md) for information on the expected conduct for contributing to Platform Mesh.

<p align="center"><img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/></p>
