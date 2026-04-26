# Kubernetes Deployment Example

These manifests are a production-oriented starting point for a single Kronos
control-plane replica with persistent embedded state and an opt-in worker agent
Deployment.

Apply the example:

```bash
kubectl apply -f deploy/kubernetes/
kubectl -n kronos rollout status deployment/kronos-control-plane
kubectl -n kronos port-forward service/kronos-control-plane 8500:8500
curl -fsS http://127.0.0.1:8500/readyz
```

The agent Deployment starts with `replicas: 0` so the example can be applied
before production secrets exist. After creating an agent token and keys, create
the secret and scale workers:

```bash
kubectl -n kronos create secret generic kronos-agent-secrets \
  --from-literal=token="$KRONOS_TOKEN" \
  --from-literal=manifest-private-key="$KRONOS_MANIFEST_PRIVATE_KEY" \
  --from-literal=chunk-key="$KRONOS_CHUNK_KEY"
kubectl -n kronos scale deployment/kronos-agent --replicas=1
kubectl -n kronos rollout status deployment/kronos-agent
```

Before using this in production:

- Replace `ghcr.io/kronosbackup/kronos:latest` with an immutable image digest.
- Replace the sample `kronos.yaml` with your targets, storages, schedules, and
  auth settings.
- Keep `replicas: 1` unless the state backend is moved to a shared,
  concurrency-safe service.
- Configure backup repository credentials with Kubernetes Secrets or external
  secret injection. See
  [Cloud Secret Integration](../../docs/cloud-secrets.md) for managed secret
  store patterns.
- Keep agent `replicas` and `--capacity` aligned with each database target's
  safe backup concurrency.
- Review the included NetworkPolicy and tighten allowed namespaces/pods for
  your cluster.
- Keep the container security contexts unless your runtime requires a reviewed
  exception.
- Add TLS termination and RBAC appropriate for your cluster.
