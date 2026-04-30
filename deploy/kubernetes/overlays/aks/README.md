# AKS Overlay

Use this overlay as a starting point for Azure Kubernetes Service clusters:

```bash
kubectl apply -k deploy/kubernetes/overlays/aks
```

Before applying in production, replace the Azure workload identity client ID
placeholder in `serviceaccount.yaml` and pin the Kronos image to an immutable
digest by copying the `images` block from
`../immutable-image/kustomization.yaml` into this overlay's
`kustomization.yaml`.
