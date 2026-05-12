> [!WARNING]
> This Repository is under development and not ready for productive use. It is in an alpha stage. That means APIs and concepts may change on short notice including breaking changes or complete removal of apis.

# platform-mesh-operator
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/platform-mesh/platform-mesh-operator/badge)](https://scorecard.dev/viewer/?uri=github.com/platform-mesh/platform-mesh-operator)

The platform-mesh-operator bootstraps and reconciles platform-mesh environments. It reconciles a `Kind: PlatformMesh` resource which looks like this:

```yaml
apiVersion: core.platform-mesh.io/v1alpha1
kind: PlatformMesh
metadata:
  name: platform-mesh-sample
  namespace: platform-mesh-system
spec:
  kcp:
    adminSecretRef:
      name: platform-mesh-kcp-internal-admin-kubeconfig
    providerConnections:
    - endpointSliceName: core.platform-mesh.io
      path: root:platform-mesh-system
      secret: platform-mesh-operator-kubeconfig
      adminAuth: true
```

## PlatformMesh Resource Configuration

The `PlatformMesh` resource provides a comprehensive way to configure your platform-mesh environment. Below is a detailed explanation of each section and field available in the resource specification:

### Profile ConfigMap

The operator reads its deployment configuration from a profile ConfigMap. By default it looks for a ConfigMap named `<instance-name>-profile` in the instance namespace. This can be overridden:

```yaml
spec:
  profileConfigMap:
    name: platform-mesh-profile
    namespace: platform-mesh-system
```

The ConfigMap must contain a `profile.yaml` key with two top-level sections: `infra` and `components`. The operator renders Go templates inside the profile at reconcile time, substituting variables like `{{ .baseDomainPort }}` and `{{ .baseDomain }}` from the exposure configuration.

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

The `kcp` section manages KCP (Kubernetes Control Plane) setup and connections:

#### Provider Connections

Provider connections define how platform-mesh connects to provider workspaces:

```yaml
spec:
  kcp:
    providerConnections:
    - endpointSliceName: core.platform-mesh.io   # APIExportEndpointSlice name (for admin auth)
      path: root:platform-mesh-system            # Path in KCP workspace hierarchy
      secret: provider-kubeconfig                # Secret to store kubeconfig
      adminAuth: true                            # Use admin cert-based auth (default: true)

    # Scoped provider connections (uses ServiceAccount token + RBAC from APIExport)
    - apiExportName: core.platform-mesh.io       # APIExport name (for scoped auth)
      path: root:platform-mesh-system
      secret: scoped-kubeconfig
      adminAuth: false                           # Use scoped kubeconfig

    # Additional provider connections
    extraProviderConnections:
    - endpointSliceName: auxiliary.platform-mesh.io
      path: root:auxiliary-system
      secret: auxiliary-kubeconfig
```

#### Extra Workspaces

```yaml
spec:
  kcp:
    extraWorkspaces:
    - path: "root:orgs:my-new-workspace"
      type:
        name: "universal"
        path: "root"
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
```

### Values and InfraValues

Custom values can be provided for components and infra respectively:

```yaml
spec:
  values:
    key1: value1
  infraValues:
    key2: value2
```

These are merged with the profile's `components` and `infra` sections when rendering Go templates.

### Feature Toggles

Certain features can be enabled or disabled using feature toggles in the PlatformMesh resource specification:

```yaml
spec:
  featureToggles:
  - name: "<feature-name>"
    parameters:
      key: value
```

#### Available Feature Toggles

| Feature Toggle Name | Description |
|---------------------|-------------|
| `feature-enable-getting-started` | Applies the ContentConfiguration resources required for the Getting Started UI page |
| `feature-accounts-in-accounts` | Applies the ContentConfiguration resources for displaying accounts within the account context |
| `feature-enable-account-iam-ui` | Applies the ContentConfiguration resources for the IAM UI Members section at the account level |
| `feature-disable-email-verification` | Disables email verification requirement in WorkspaceAuthenticationConfiguration |
| `feature-disable-contentconfigurations` | Disables loading of all ContentConfiguration manifests during KCP setup |

### Wait Configuration

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

If `spec.wait` is not specified, the subroutine uses default configurations that wait for the `platform-mesh-operator-infra-components` HelmRelease to be ready.

## Architecture

The operator uses a subroutine-based architecture (`github.com/platform-mesh/subroutines`) with a lifecycle manager that executes subroutines **sequentially in a fixed order**. If any subroutine returns an error or explicitly stops the chain, the remaining subroutines are skipped and the reconcile loop is retried after a requeue interval.

### Subroutine Execution Order

The subroutines run in the following order on every reconcile:

1. **Deployment** — renders Go templates and applies infra/component resources (HelmReleases, ArgoCD Applications, OCM Resources)
2. **KcpSetup** — creates KCP workspaces and applies `manifests/kcp/` to them
3. **ProviderSecret** — creates workspace-scoped kubeconfig secrets for all `providerConnections`
4. **FeatureToggles** — applies feature-gated KCP manifests
5. **Wait** — waits for deployment resources (e.g., HelmReleases) to reach a ready state

The ordering is significant:

- **Deployment runs first** so that infra components (cert-manager, KCP operator, etc.) are applied before any subroutine that depends on them being available in the cluster.
- **KcpSetup runs before ProviderSecret** because the KCP workspaces must exist before kubeconfig secrets can be written into them.

### Go Templates

The operator renders deployment manifests directly from Go templates located in:
- `gotemplates/infra/` — infrastructure components (cert-manager, traefik, gateway-api, etcd-druid, kcp-operator)
- `gotemplates/components/` — application components (HelmReleases, OCM Resources for each service)

These templates are rendered using the profile ConfigMap data merged with exposure-derived template variables (`baseDomain`, `baseDomainPort`, `port`, `protocol`). The gotemplates replace the previously used `platform-mesh-operator-components` and `platform-mesh-operator-infra-components` Helm charts.

### Deployment Technologies

The operator supports two deployment technologies (configured per-section in the profile):
- **FluxCD** (`fluxcd`): Creates HelmRelease and OCM Resource objects. FluxCD reconciles them into the cluster.
- **ArgoCD** (`argocd`): Creates ArgoCD Application objects. The ResourceSubroutine manages OCI repository references for ArgoCD.

## Subroutines

The platform-mesh-operator processes the PlatformMesh resource through several subroutines:

### Deployment

The Deployment subroutine manages the deployment of platform-mesh components:

- Reads the profile ConfigMap and renders Go templates from `gotemplates/infra/` and `gotemplates/components/`
- Creates OCM Resources, HelmReleases (or ArgoCD Applications) for each enabled service
- Manages authorization webhook secrets (issuer, certificate, KCP webhook secret with CA bundle)
- Waits for cert-manager to be ready before proceeding
- Optionally waits for Istio istiod and ensures the operator pod has an istio-proxy sidecar
- Waits for KCP `RootShard` and `FrontProxy` to become available

### KcpSetup

The KcpSetup subroutine handles initialization of the KCP environment:

- Creates workspaces based on paths in `providerConnections`
- Applies KCP manifests (APIExports, APIResourceSchemas, ContentConfigurations, etc.) from `manifests/kcp/`
- Sets up API bindings as specified in `extraDefaultAPIBindings`
- Creates extra workspaces specified in `spec.kcp.extraWorkspaces`

### ProviderSecret

The ProviderSecret subroutine manages kubeconfig secrets for provider connections:

- **Admin auth mode** (`adminAuth: true`): Reads the admin kubeconfig from `kcp.adminSecretRef`, resolves the endpoint URL from the APIExportEndpointSlice, appends the root CA, and writes the kubeconfig secret
- **Scoped auth mode** (`adminAuth: false`): Creates a ServiceAccount, ClusterRole, ClusterRoleBinding in the target workspace, generates a scoped kubeconfig with a bound token

### FeatureToggles

The FeatureToggles subroutine applies or removes KCP manifests based on enabled feature toggles:

- Reads manifests from `manifests/features/<feature-name>/`
- Applies them to the appropriate KCP workspace paths
- Supports parameterized features via `parameters` map

### Wait

The Wait subroutine ensures specified resources are ready before marking reconciliation complete:

- Waits for resources to match specific conditions (e.g., HelmRelease with `Ready=True`)
- Uses configurable wait criteria from `spec.wait` or defaults
- Supports label selectors, namespace filtering, and custom condition types
- Supports status field path matching for non-standard resources

### Resource (ResourceSubroutine)

The Resource subroutine (in `pkg/subroutines/resource/`) manages OCM Resource objects for the deployment:

- Watches OCM Resource objects and reconciles them based on deployment technology
- For FluxCD: creates OCIRepository → HelmRelease chain with chartRef
- For ArgoCD: updates Application objects with resolved OCI repository URLs from OCM Resources
- Manages image version extraction and stores versions in the ImageVersionStore

## Releasing

The release is performed automatically through a GitHub Actions Workflow.
All the released versions will be available through access to GitHub (as any other Golang Module).

## Requirements

The platform-mesh-operator requires a installation of go. Checkout the [go.mod](go.mod) for the required go version and dependencies.

## Contributing

Please refer to the [CONTRIBUTING.md](CONTRIBUTING.md) file in this repository for instructions on how to contribute to Platform Mesh.

## Code of Conduct

Please refer to our [Code of Conduct](https://github.com/platform-mesh/.github/blob/main/CODE_OF_CONDUCT.md) for information on the expected conduct for contributing to Platform Mesh.

<p align="center"><img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/></p>
