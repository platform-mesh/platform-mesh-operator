# openmfp-operator

The openmfp-operator helps bootstrap new openmfp environment during initial setup. It does so by reconciling and `Kind: OpenMFP` resource which looks like this

```yaml
apiVersion: core.openmfp.org/v1alpha1
kind: OpenMFP
metadata:
  labels:
  name: openmfp-sample
  namespace: openmfp-system
spec:
  kcp:
    adminSecretRef:
      name: openmfp-kcp-internal-admin-kubeconfig
    providerConnections:
    - endpointSliceName: core.openmfp.org
      path: root:openmfp-system
      secret: openmfp-operator-kubeconfig
```

The `adminSecretRef` points to a secret containking a KCP kubeconfig, which is used by the operator to create the KCP workspaces and exports. After the operator finishes with the setup stage of KCP, it creates additional secrets for each `providerConnection` object configured in the resource above.