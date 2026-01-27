> [!WARNING]
> This Repository is under development and not ready for productive use. It is in an alpha stage. That means APIs and concepts may change on short notice including breaking changes or complete removal of apis.

# platform-mesh-operator

The platform-mesh-operator helps bootstrap new platform-mesh environment during initial setup. It does so by reconciling and `Kind: PlatformMesh` resource which looks like this

```yaml
apiVersion: core.platform-mesh.io/v1alpha1
kind: PlatformMesh
metadata:
  name: platform-mesh-sample
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
    - name: "core"
  kcp:
    providerConnections:
    - endpointSliceName: core.platform-mesh.io
      path: root:platform-mesh-system
      secret: platform-mesh-operator-kubeconfig
    initializerConnections:
    - workspaceTypeName: universal
      path: root:initializers
      secret: initializer-kubeconfig
    extraWorkspaces:
    - path: "root:orgs:my-new-workspace"
      type:
        name: "universal"
        path: "root"
    extraProviderConnections:
    - path: "root:orgs:my-new-workspace"
      secret: "my-new-workspace-kubeconfig"
  values:
    service1:
      enabled: true
      targetNamespace: default
      values:
        type: None
    service2:
      enabled: false
```

## platform-mesh-operator Configuration

The platform-mesh-operator can be configured using environment variables or command-line parameters to control its behavior, cluster interactions, and subroutine execution. Command-line parameters use kebab-case format (e.g., `--workspace-dir`, `--kcp-url`) corresponding to the mapstructure tags in the configuration.


### General Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `KUBECONFIG` | Path to the kubeconfig file for the cluster where the `PlatformMesh` resource is reconciled | In-cluster configuration |
| `WORKSPACE_DIR` | Working directory for operator files and temporary data | `/operator/` |
| `PATCH_OIDC_CONTROLLER_ENABLED` | Enable the OIDC controller patching functionality | `false` |
| `LEADER_ELECTION_ID` | Leader election ID for the main manager instance | `81924e50.platform-mesh.org` |

### KCP Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `KCP_URL` | URL of the KCP API server | (required) |
| `KCP_NAMESPACE` | Namespace where KCP components are deployed | `platform-mesh-system` |
| `KCP_ROOT_SHARD_NAME` | Name of the KCP root shard | `root` |
| `KCP_FRONT_PROXY_NAME` | Name of the KCP front proxy component | `frontproxy` |
| `KCP_FRONT_PROXY_PORT` | Port for the KCP front proxy | `6443` |
| `KCP_CLUSTER_ADMIN_SECRET_NAME` | Name of the secret containing KCP cluster admin client certificate | `kcp-cluster-admin-client-cert` |

### Subroutines Configuration

#### Deployment Subroutine

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_DEPLOYMENT_ENABLED` | Enable the deployment subroutine | `true` |
| `AUTHORIZATION_WEBHOOK_SECRET_NAME` | Name of the authorization webhook secret | `kcp-webhook-secret` |
| `AUTHORIZATION_WEBHOOK_SECRET_CA_NAME` | Name of the authorization webhook CA certificate | `rebac-authz-webhook-cert` |
| `SUBROUTINES_DEPLOYMENT_ENABLE_ISTIO` | Enable Istio integration in deployment subroutine | `true` |

#### KCP Setup Subroutine

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_KCP_SETUP_ENABLED` | Enable the KCP setup subroutine | `true` |

#### Provider Secret Subroutine

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_PROVIDER_SECRET_ENABLED` | Enable the provider secret subroutine | `true` |

#### Patch OIDC Subroutine

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_PATCH_OIDC_CONFIGMAP_NAME` | Name of the OIDC authentication ConfigMap | `oidc-authentication-config` |
| `SUBROUTINES_PATCH_OIDC_NAMESPACE` | Namespace for OIDC configuration | `platform-mesh-system` |
| `SUBROUTINES_PATCH_OIDC_BASEDOMAIN` | Base domain for OIDC configuration | `portal.dev.local:8443` |
| `SUBROUTINES_PATCH_OIDC_DOMAIN_CA_LOOKUP` | Enable domain CA lookup for OIDC | `false` |

#### Feature Toggles Subroutine

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_FEATURE_TOGGLES_ENABLED` | Enable the feature toggles subroutine | `false` |

#### Resource Subroutine

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_RESOURCE_ENABLED` | Enable the resource subroutine | `true` |

#### Wait Subroutine

| Variable | Description | Default |
|----------|-------------|---------|
| `SUBROUTINES_WAIT_ENABLED` | Enable the wait subroutine | `true` |

### Remote Infrastructure Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `REMOTE_INFRA_ENABLED` | Enable reconciliation of infrastructure resources on a remote cluster | `false` |
| `REMOTE_INFRA_KUBECONFIG` | Path to the kubeconfig for remote infrastructure cluster | `/operator/infra-kubeconfig` |

### Remote Runtime Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `REMOTE_RUNTIME_ENABLED` | Enable reconciliation of PlatformMesh resource on a remote runtime cluster | `false` |
| `REMOTE_RUNTIME_KUBECONFIG` | Path to the kubeconfig for remote runtime cluster | `/operator/runtime-kubeconfig` |
| `REMOTE_RUNTIME_INFRA_SECRET_NAME` | Name of the secret containing infra kubeconfig in the remote runtime cluster | `infra-kubeconfig` |
| `REMOTE_RUNTIME_INFRA_SECRET_KEY` | Key in the secret containing infra kubeconfig in the remote runtime cluster | `kubeconfig` |

### Configuration Notes

- **Configuration methods**: All parameters can be set via environment variables (using underscore-separated uppercase names) or command-line flags (using kebab-case format, e.g., `--kcp-url`, `--workspace-dir`).
- **In-cluster behavior**: When running the operator inside a Kubernetes cluster without `KUBECONFIG` or `DEPLOYMENT_KUBECONFIG` set, it will use the in-cluster service account credentials.
- **Remote deployment**: Setting `DEPLOYMENT_KUBECONFIG` enables scenarios where the control plane (operator) runs in one cluster while deploying components to another cluster.
- **Subroutine control**: Individual subroutines can be disabled by setting their respective `_ENABLED` variables to `false`, allowing fine-grained control over operator behavior.

## PlatformMesh Resource Configuration

The `PlatformMesh` resource provides a comprehensive way to configure your platform-mesh environment. Below is a detailed explanation of each section and field available in the resource specification:

### Exposure Configuration

The `exposure` section configures how services are exposed externally:

```yaml
spec:
  exposure:
    baseDomain: example.com       # Base domain for exposure
    port: 443                     # Port to expose services on
    protocol: https               # Protocol (http/https)
```

### KCP Configuration

The `kcp` section manages Kubernetes Control Plane setup and connections:

#### Provider Connections

Provider connections define how platform-mesh connects to provider Kubernetes clusters:

```yaml
spec:
  kcp:
    providerConnections:
    - endpointSliceName: core.platform-mesh.io   # Name of the endpoint slice
      path: root:platform-mesh-system            # Path in KCP workspace hierarchy
      secret: provider-kubeconfig                # Secret to store connection information
      external: false                            # Whether this is an external provider
    
    # Additional provider connections can be configured
    extraProviderConnections:
    - endpointSliceName: auxiliary.platform-mesh.io
      path: root:auxiliary-system
      secret: auxiliary-kubeconfig
```

#### Initializer Connections

Initializer connections are used to set up workspaces with specific types:

```yaml
spec:
  kcp:
    initializerConnections:
    - workspaceTypeName: universal         # The workspace type to use
      path: root:initializers              # Path in KCP workspace hierarchy
      secret: initializer-kubeconfig       # Secret for connection
    
    extraInitializerConnections:
    - workspaceTypeName: specialized
      path: root:extra-initializers
      secret: extra-initializer-kubeconfig
```

#### Default API Bindings

Configure additional default API bindings for workspaces:

```yaml
spec:
  kcp:
    extraDefaultAPIBindings:
    - workspaceTypePath: root:types
      export: services
      path: root:exports
```

### OCM Configuration

The `ocm` section configures Open Component Model integration:

```yaml
spec:
  ocm:
    repo:
      name: platform-mesh              # Repository name (defaults to "platform-mesh")
    component:
      name: platform-mesh              # Component name (defaults to "platform-mesh")
    referencePath:                     # Path of references to follow
    - name: core
    - name: services
```

### Values Configuration

Custom values can be provided:

```yaml
spec:
  values: 
    key1: value1
    nested:
      key2: value2
```

Those values are passed 1-1 to the chart deployed by the "Deployment" subroutine.

### Profile Configuration

The deployment profile controls the configuration of infrastructure and component deployments. The profile is stored in a ConfigMap and can be customized per PlatformMesh instance.

#### Default Profile

If no custom profile is specified, the operator automatically creates a default ConfigMap named `<platform-mesh-name>-profile` in the same namespace as the PlatformMesh resource. This default ConfigMap contains a unified profile with two sections:

- **infra**: Infrastructure components configuration (gateway-api, traefik, cert-manager, etc.)
- **components**: Runtime components configuration (account-operator, security-operator, keycloak, etc.)

#### Custom Profile

You can specify a custom profile ConfigMap by referencing it in the PlatformMesh spec:

```yaml
spec:
  profileConfigMap:
    name: my-custom-profile
    namespace: platform-mesh-system  # Optional, defaults to PlatformMesh namespace
```

The ConfigMap must contain a key `profile.yaml` with the unified profile structure:

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
  # ... other infra configuration

components:
  targetNamespace: platform-mesh-system
  protocol: https
  port: 443
  services:
    account-operator:
      enabled: true
      values:
        # ... service configuration
    # ... other services
```

The profile structure matches the original `profile-infra.yaml` and `profile-components.yaml` files, now unified into a single structure with `infra` and `components` sections.

### Feature Toggles

Certain features can be enabled or disabled using feature toggles in the PlatformMesh resource specification. Feature toggles are configured as follows:

```yaml
spec:
  featureToggles:
  - name: "<feature-name>"
```

#### Available Feature Toggles

| Feature Toggle Name | Description |
|---------------------|-------------|
| `feature-enable-getting-started` | Applies the ContentConfiguration resources required for the Getting Started UI page |
| `feature-enable-marketplace-account` | Applies the ContentConfiguration resources for the Marketplace feature at the account level |
| `feature-enable-marketplace-org` | Applies the ContentConfiguration resources for the Marketplace feature at the organization level |
| `feature-accounts-in-accounts` | Applies the ContentConfiguration resources for displaying accounts within the account context |
| `feature-disable-email-verification` | Disables email verification requirement in WorkspaceAuthenticationConfiguration |
| `feature-disable-contentconfigurations` | Disables loading of all ContentConfiguration manifests during KCP setup |

#### Example Usage

```yaml
apiVersion: core.platform-mesh.io/v1alpha1
kind: PlatformMesh
metadata:
  name: platform-mesh-sample
  namespace: platform-mesh-system
spec:
  featureToggles:
  - name: "feature-enable-getting-started"
  - name: "feature-enable-marketplace-account"
  - name: "feature-disable-email-verification"
  # ... other configuration
```


## Subroutines

The platform-mesh-operator processes the PlatformMesh resource through several subroutines:

### Deployment

The Deployment subroutine manages the deployment of platform-mesh components across the cluster:

- Loads deployment profiles from ConfigMap (or creates a default one if not specified).
- Merges custom values from the `PlatformMesh` resource with profile configurations.
- Applies templated manifests and waits for the HelmRelease to become ready and also for `cert-manager` to become ready.
- Applies templated Kubernetes manifests, including `Resource` and `HelmRelease` objects.
- Manages OCM (Open Component Model) integration by configuring resources based on repository, component, and reference path settings.
- Manages authorization webhook secrets by creating an issuer, a certificate, and a KCP webhook secret, and keeps the secret updated with the correct CA bundle.
- Waits for the `istio-istiod` Helm release to become ready.
- Checks for the Istio sidecar proxy in the operator's own pod and triggers a restart if it's not present to ensure proper communication with KCP.
- Waits for KCP components like `RootShard` and `FrontProxy` to become available.

#### Merging of custom values in `DeploymentSubroutine`

When creating helmreleases, their configuration is derived from the **PlatformMesh** resource as follows:

- HelmRelease has `spec.values` which is equal to the `PlatformMesh.Spec.Values` after replacing templated values.
- Resource `spec.componentRef` is set to point to `PlatformMesh.Spec.OCM.Component.Name`

For both HelmReleases the spec.values are populated with these templated fields:
- baseDomain
- baseDomainPort
- iamWebhookCA
- port
- protocol


### KcpSetup

The KcpSetup subroutine handles the initialization of the KCP environment:

- Creates workspaces based on the specified paths in `providerConnections` and `initializerConnections`
- Sets up API bindings as specified in `extraDefaultAPIBindings`
- Create extra Workspaces specified in the `spec.KCP.extraWorkspaces`

### ProviderSecret

The ProviderSecret subroutine manages the creation and maintenance of secrets for provider connections:

- Creates secrets for each provider connection specified in the `providerConnections` and `extraProviderConnections` sections
- Updates the secrets when configurations change
- Manages access credentials for connecting to provider clusters

### Defaults

The Defaults subroutine applies default configurations when specific fields are not explicitly set:

- Applies default values for `ocm.repo.name` and `ocm.component.name`
- Sets up default configurations for the platform-mesh environment
- Ensures a consistent baseline configuration

### Webhook

The Webhook subroutine handles webhook configurations for the platform-mesh:

- Sets up and manages webhook configurations for API validation and mutation
- Configures webhook secrets and references as defined in the configuration
- Ensures proper webhook functionality for platform-mesh resources

### Wait

The Wait subroutine ensures that specified resources are ready before proceeding with the reconciliation:

- Waits for resources to match specific conditions (e.g., HelmRelease resources with Ready condition)
- Uses configurable wait criteria defined in the `spec.wait` section of the PlatformMesh resource
- Falls back to default wait configurations when no custom wait configuration is specified
- By default, waits for HelmRelease resources to be ready
- Supports filtering resources by namespace, labels, and API versions
- Requeues the reconciliation if any monitored resource is not yet ready

#### Wait Configuration

The wait behavior can be customized through the `spec.wait` section:

```yaml
spec:
  wait:
    resourceTypes:
    - apiVersions:
        versions: ["v2"]
      groupKind:
        group: "helm.toolkit.fluxcd.io"
        kind: "HelmRelease"
      namespace: "default"
      labelSelector:
        matchExpressions:
        - key: "helm.toolkit.fluxcd.io/name"
          operator: In
          values: ["my-release"]
      conditionStatus: "True"
      conditionType: "Ready"
```

If `spec.wait` is not specified, the subroutine uses default configurations that wait for the core platform-mesh HelmRelease resources.


## Releasing

The release is performed automatically through a GitHub Actions Workflow.
All the released versions will be available through access to GitHub (as any other Golang Module).

## Requirements

The platform-mesh-operator requires a installation of go. Checkout the [go.mod](go.mod) for the required go version and dependencies.

## Contributing

Please refer to the [CONTRIBUTING.md](CONTRIBUTING.md) file in this repository for instructions on how to contribute to Platform Mesh.

## Code of Conduct

Please refer to the [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) file in this repository information on the expected Code of Conduct for contributing to Platform Mesh.

## Licensing

Copyright 2024 SAP SE or an SAP affiliate company and Platform Mesh contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/platform-mesh/platform-mesh-operator).