> [!WARNING]
> This Repository is under development and not ready for productive use. It is in an alpha stage. That means APIs and concepts may change on short notice including breaking changes or complete removal of apis.

# platform-mesh-operator

The platform-mesh-operator helps bootstrap new platform-mesh environment during initial setup. It does so by reconciling and `Kind: PlatformMesh` resource which looks like this

```yaml
apiVersion: core.platform-mesh.io/v1alpha1
kind: PlatformMesh
metadata:
  labels:
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
```

The `adminSecretRef` points to a secret containking a KCP kubeconfig, which is used by the operator to create the KCP workspaces and exports. After the operator finishes with the setup stage of KCP, it creates additional secrets for each `providerConnection` object configured in the resource above.

## Subroutines

### KcpSetup
### ProviderSecret
### Defaults
### Webhook
