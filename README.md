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
spec:iam-service
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

Those values are passed 1-1 to the `platform-mesh-operator-components` chart, deployed by the "Deployment" subroutine.

### Feature toggles

Certain features can be toggled by the user via the API:

#### feature-enable-getting-started

```yaml
spec:
  featureToggles:
  - name: "feature-enable-getting-started"
    parameters: {}
```

This applies the needed ContentConfiguration for the Getting Started UI page.

## Subroutines

The platform-mesh-operator processes the PlatformMesh resource through several subroutines:

## Deployment

The Deployment subroutine manages the deployment of platform-mesh components across the cluster:

- Merges custom values from the `PlatformMesh` resource with default configurations.
- Applies templated Kubernetes manifests for `platform-mesh-operator-components`, including `Resource` and `HelmRelease` objects.
- Manages OCM (Open Component Model) integration by configuring resources based on repository, component, and reference path settings.
- Manages authorization webhook secrets by creating an issuer, a certificate, and a KCP webhook secret, and keeps the secret updated with the correct CA bundle.
- Waits for the `istio-istiod` Helm release to become ready.
- Checks for the Istio sidecar proxy in the operator's own pod and triggers a restart if it's not present to ensure proper communication with KCP.
- Waits for KCP components like `RootShard` and `FrontProxy` to become available.

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