# Kubernetes Deployment Example

These manifests are a production-oriented starting point for a single Kronos
control-plane replica with persistent embedded state.

Apply the example:

```bash
kubectl apply -f deploy/kubernetes/
kubectl -n kronos rollout status deployment/kronos-control-plane
kubectl -n kronos port-forward service/kronos-control-plane 8500:8500
curl -fsS http://127.0.0.1:8500/readyz
```

Before using this in production:

- Replace `ghcr.io/kronosbackup/kronos:latest` with an immutable image digest.
- Replace the sample `kronos.yaml` with your targets, storages, schedules, and
  auth settings.
- Keep `replicas: 1` unless the state backend is moved to a shared,
  concurrency-safe service.
- Configure backup repository credentials with Kubernetes Secrets or external
  secret injection.
- Add NetworkPolicy, TLS termination, and RBAC appropriate for your cluster.
