# EKS Overlay

Use this overlay as a starting point for Amazon EKS clusters:

```bash
kubectl apply -k deploy/kubernetes/overlays/eks
```

Before applying in production, replace the IRSA role ARN placeholder in
`serviceaccount.yaml` and pin the Kronos image to an immutable digest by copying
the `images` block from `../immutable-image/kustomization.yaml` into this
overlay's `kustomization.yaml`.
