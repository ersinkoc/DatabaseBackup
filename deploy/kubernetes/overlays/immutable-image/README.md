# Immutable Image Overlay

Use this overlay as a copyable starting point for production deployments that
pin Kronos by image digest instead of a mutable tag:

```bash
kubectl apply -k deploy/kubernetes/overlays/immutable-image
```

Before applying, replace the example `sha256:012345...` digest in
`kustomization.yaml` with the release image digest from the promoted artifact.
The `images` entry rewrites both the control-plane and agent Deployments from
`ghcr.io/kronosbackup/kronos:latest` to
`ghcr.io/kronosbackup/kronos@sha256:<digest>`.

For managed-cluster overlays, copy the same `images` block into the target
provider `kustomization.yaml` after setting the provider-specific service
account and storage class values.
