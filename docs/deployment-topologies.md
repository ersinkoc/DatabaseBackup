# Deployment Topologies

Kronos ships as one binary, but production deployments should choose the
smallest topology that keeps the control plane, agents, state, secrets, and
backup repository easy to operate.

## Decision Matrix

| Topology | Best For | Control Plane | Agents | State | Notes |
| --- | --- | --- | --- | --- | --- |
| Local single node | Labs, break-glass local backup | `kronos local --work` | Embedded | Local disk | Fastest bootstrap; keep it bound to localhost. |
| Split server and agents | Small production fleets | One `kronos server` | One or more `kronos agent --work` | Persistent control-plane disk | Recommended default while the embedded state backend is local. |
| Kubernetes single replica | Containerized production | One Deployment replica | Separate agent Deployments or external agents | PVC | Good operational wrapper; avoid multiple control-plane replicas until state is externalized. |
| Hot standby agents | Critical targets | One control plane | Two agents can see the same target | Persistent control-plane disk | Agents are stateless; job claiming keeps one worker active per queued job. |

## Local Single Node

```mermaid
flowchart LR
    Operator[kronos CLI]
    Local[kronos local --work]
    State[(state.db)]
    Repo[(backup repository)]
    DB[(database)]

    Operator --> Local
    Local --> State
    Local --> DB
    Local --> Repo
```

Use this topology for development, home lab setups, and emergency local
operations. It minimizes moving parts by running the control plane and worker in
one process. In production, bind it to loopback or put it behind a trusted
private network and still use scoped tokens for automation.

## Split Control Plane And Agent Fleet

```mermaid
flowchart TB
    CLI[kronos CLI / automation]
    Server[kronos server]
    State[(persistent state.db)]
    AgentA[kronos agent --work]
    AgentB[kronos agent --work]
    RedisA[(Redis primary)]
    RedisB[(Redis replica)]
    Repo[(local or S3-compatible repository)]

    CLI --> Server
    Server --> State
    AgentA --> Server
    AgentB --> Server
    AgentA --> RedisA
    AgentB --> RedisB
    AgentA --> Repo
    AgentB --> Repo
```

This is the recommended default for production. Agents run near the databases,
dial the control plane, claim queued jobs, stream directly to storage, and can
be restarted without losing control-plane state. Keep the control-plane state
directory on reliable storage and back it up like any other operational
database.

## Kubernetes Single Replica

```mermaid
flowchart TB
    subgraph Cluster[Kubernetes namespace kronos]
        Service[Service :8500]
        ControlPlane[Deployment kronos-control-plane replicas=1]
        PVC[(PVC /var/lib/kronos)]
        Config[ConfigMap kronos.yaml]
    end

    Operator[kubectl / CLI]
    Agent[agent deployment or external agent]
    Repo[(backup repository)]

    Operator --> Service
    Service --> ControlPlane
    ControlPlane --> PVC
    Config --> ControlPlane
    Agent --> Service
    Agent --> Repo
```

Start from [deploy/kubernetes](../deploy/kubernetes/README.md). Keep
`replicas: 1` for the control plane while it uses embedded local state. The
example agent Deployment starts at `replicas: 0`; create
`kronos-agent-secrets`, then scale agents deliberately. Scale workers by adding
agents, not by adding control-plane replicas. Use Kubernetes Secrets or an
external secret injector for manifest signing keys, chunk keys, tokens, and
repository credentials. The example control-plane Deployment uses `strategy:
Recreate`, a single PVC, and a `PodDisruptionBudget` with `minAvailable: 1`;
planned node drains may need an explicit maintenance window because Kronos
does not yet support multiple active control-plane writers.

## Hot Standby Agents

```mermaid
sequenceDiagram
    participant S as Control plane
    participant A as Agent A
    participant B as Agent B
    participant R as Repository

    A->>S: heartbeat capacity=1
    B->>S: heartbeat capacity=1
    S->>A: claim queued backup job
    A->>R: stream chunks and manifest
    B->>S: claim attempt returns no job
    A->>S: finish succeeded
```

Use hot standby agents when a target is critical and workers may be restarted
during maintenance. Both agents can be configured for the same target and
storage, but only the agent that successfully claims a job executes it. Keep
agent capacity conservative for databases that should not run parallel backup
work.

## Production Guardrails

- Use immutable release artifacts or image digests, not mutable `latest` tags.
- Keep the control-plane state directory on durable storage and include it in
  backup/restore exercises.
- Store signing keys, chunk keys, bearer tokens, and repository credentials in a
  secret manager rather than plain config files. See
  [Cloud Secret Integration](cloud-secrets.md) for managed secret store
  patterns.
- Apply NetworkPolicy rules around the control plane and agents before exposing
  the service outside a trusted namespace.
- Run `kronos ready`, scrape `/metrics`, and alert on backup freshness before
  enabling unattended schedules.
- Exercise at least one restore path after every material driver, storage, or
  key-management change.
- Prefer scaling agents horizontally before changing the control-plane topology.
