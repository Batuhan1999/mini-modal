# MiniModal learning roadmap

Last reviewed: 2026-09-24

This is a guided reading and watching list for understanding how a Modal-,
Daytona-, or E2B-like sandbox platform works. It focuses on the parts that map
directly to MiniModal: a public API, a distributed control plane, scheduling,
node agents, sandbox execution, persistence, failure recovery, warm pools, and
eventually GPUs.

Do not try to consume everything before continuing the project. Follow the
core path alongside implementation, then use the reference sections when a
feature becomes relevant.

This is a comprehensive technical curriculum, not a mirror of every marketing
post or SDK method. The company sections include the official content hubs so
you can discover newer material after this document's review date.

## The system you are learning to build

```text
                                   CONTROL PLANE

 client / SDK
      |
      v
 load balancer -> API replicas -> PostgreSQL (durable desired/current state)
                         |             |
                         |             +-> event/outbox/assignment queue
                         v
                 scheduler + reconcilers
                         |
                         | assignments, leases, cancellation
                         v
              agent connections / RPC gateway

 ---------------------------------------------------------------------------

                                   DATA PLANE

 node agent <---- outbound stream ---- control plane
      |
      +-> executor -> container / gVisor / microVM -> user process
      |
      +-> heartbeat, capacity, logs, status and exit code

 shared supporting systems:
 object storage, image registry, logs, metrics, traces, secrets and networking
```

The control plane owns durable intent and global decisions. The data plane owns
the local reality of starting, supervising and stopping workloads. The database
is initially the source of truth. A queue transports work but should not become
the only place where job state exists.

## Short path: the twelve highest-value resources

Read or watch these first, in this order.

1. [E2B Runtime overview](https://github.com/e2b-dev/runtime) — the closest
   public reference implementation to MiniModal. It is written primarily in Go
   and shows the API, orchestrator, Firecracker runtime, in-VM agent, proxy and
   storage systems.
2. [E2B infrastructure architecture](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)
   — read every diagram and lifecycle sequence. This is the single most useful
   document in this list.
3. [Daytona architecture](https://www.daytona.io/docs/en/architecture/) — a
   clear control-plane/compute-plane split with runners, polling, PostgreSQL,
   Redis, snapshots, volumes and proxy routing.
4. [Modal: scaling to one million concurrent sandboxes](https://modal.com/blog/scaling-to-1-million-concurrent-sandboxes-in-seconds)
   — shows why central scans and serialized scheduling paths stop scaling, and
   how Modal traded global coordination for horizontal scalability.
5. [Nomad architecture](https://developer.hashicorp.com/nomad/docs/architecture)
   — servers, clients, heartbeats, allocations, RPC and leader responsibilities.
6. [How Nomad scheduling works](https://developer.hashicorp.com/nomad/docs/concepts/scheduling/how-scheduling-works)
   — jobs, evaluations, allocation plans, feasibility checks and ranking.
7. [Kubernetes controllers](https://kubernetes.io/docs/concepts/architecture/controller/)
   — the best short explanation of desired state, actual state and reconciliation.
8. [Making retries safe with idempotent APIs](https://aws.amazon.com/builders-library/making-retries-safe-with-idempotent-APIs/)
   — required reading before making Submit, Claim, Complete and Fail network calls.
9. [Timeouts, retries and backoff with jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
   — what to do when agents and control-plane replicas fail partially.
10. [Firecracker architecture and design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)
    — understand what a microVM runtime provides and what an orchestrator must
    still build around it.
11. [Go Concurrency Patterns: Context](https://go.dev/blog/context) — connects
    directly to cancellation and deadlines crossing MiniModal's service and RPC
    boundaries.
12. [Martin Kleppmann's distributed systems lecture series](https://www.youtube.com/playlist?list=PLeKd45zvjcDFUEv_ohr_HdUFe97RItdiB)
    — the best free conceptual foundation for RPC, partial failure, replication,
    consistency and consensus.

## An eight-stage learning path

Each stage contains a goal, core material, and questions to answer in MiniModal.
The times are approximate and deliberately leave room for taking notes.

### Stage 1 — Product and workload mental models (2–3 hours)

Goal: distinguish a function invocation, batch job, long-lived sandbox and
server process.

Read:

- [Modal Functions](https://modal.com/docs/guide/functions) — queued inputs,
  reusable containers and automatic scaling.
- [Modal Sandboxes](https://modal.com/docs/guide/sandboxes) — arbitrary commands,
  lifecycle events, images, volumes and detached execution.
- [Daytona Sandboxes](https://www.daytona.io/docs/sandboxes) — composable,
  long-lived computers with start, stop, pause, archive and resize operations.
- [Daytona scaling](https://www.daytona.io/docs/en/scale/) — vertical sandbox
  resizing, fleet scaling and process concurrency.
- [E2B product model](https://e2b.dev/) — one isolated microVM per agent session.

Watch:

- [Giving Agents Computers — Ivan Burazin, Daytona](https://www.youtube.com/watch?v=kaX43RRRUKY)
  (about 71 minutes). Especially watch 12:56–33:31 for bare metal, scheduling,
  bursty workloads and why an agent needs a whole computer.
- [Why AI infrastructure must evolve for agent experience — Modal](https://www.youtube.com/watch?v=UwxxlTNPjWo)
  (about 58 minutes). This covers sandboxes, bursty workloads, networking,
  distributed training and multi-cloud capacity.

Answer for yourself:

- Is a MiniModal workload one command, one function call, or a computer that
  remains available for many commands?
- When does billing/execution time begin: created, assigned, started or ready?
- Which state must survive a control-plane restart?

### Stage 2 — Go concurrency and service boundaries (3–5 hours)

Goal: understand the language mechanisms that will implement agents, servers,
streams and cancellation.

Read/watch:

- [Go Concurrency Patterns](https://go.dev/talks/2012/concurrency.slide) — the
  classic talk on goroutines, channels and structuring concurrent services.
- [Pipelines and cancellation](https://go.dev/blog/pipelines) — closing channels,
  preventing blocked goroutines and propagating cancellation.
- [Go Concurrency Patterns: Context](https://go.dev/blog/context) — cancellation,
  deadlines and request-scoped values.
- [Contexts and structs](https://go.dev/blog/context-and-structs) — why public
  methods should generally receive `context.Context` instead of storing it.
- [Official gRPC introduction](https://grpc.io/docs/what-is-grpc/introduction/)
  and [Go quick start](https://grpc.io/docs/languages/go/quickstart/).

Connect this to MiniModal:

- HTTP request context should reach `Service.Submit` and database calls.
- Agent `Claim`, `Complete` and `Fail` calls need deadlines.
- Executor cancellation should terminate the child process or container.
- A control-plane shutdown should stop accepting work, drain requests and cancel
  only the work that policy says should be cancelled.

### Stage 3 — Control plane, data plane and reconciliation (4–6 hours)

Goal: understand what each process owns and why durable desired state is more
important than an in-memory command.

Read closely:

- [E2B architecture](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)
  — compare its API, orchestrator, `envd`, client proxy and stores with MiniModal.
- [Daytona architecture](https://www.daytona.io/docs/en/architecture/) — note
  that runners poll for operations; polling is valid when backed by indexed work
  and robust lifecycle state.
- [Kubernetes controllers](https://kubernetes.io/docs/concepts/architecture/controller/)
  — desired versus actual state and independent control loops.
- [Nomad architecture](https://developer.hashicorp.com/nomad/docs/architecture)
  — server/client communication, registration, heartbeats and allocations.
- [Knative Serving architecture](https://knative.dev/docs/serving/architecture/)
  — a useful split between activator, autoscaler, controller and request path.

Inspect source:

- [E2B Runtime repository](https://github.com/e2b-dev/runtime), especially
  `packages/api`, `packages/orchestrator`, `packages/envd`, `packages/client-proxy`,
  `packages/db` and the protobuf definitions.
- [E2B local development guide](https://github.com/e2b-dev/runtime/blob/main/DEV-LOCAL.md)
  to see how a real multi-service Go repository is operated locally.

MiniModal design exercise:

```text
desired: sandbox should be running on node-a
actual:  node-a has not acknowledged it
action:  retry, expire lease, or assign elsewhere
```

Write down who is allowed to change each state and which transition is safe to
retry. This becomes the lifecycle state machine.

### Stage 4 — Scheduling and queues (4–7 hours)

Goal: move from “loop over every job and node” toward event-driven scheduling
with indexed queues and explicit allocation plans.

Read:

- [How Nomad scheduling works](https://developer.hashicorp.com/nomad/docs/concepts/scheduling/how-scheduling-works)
  — evaluations are generated when desired or observed state changes.
- [Nomad scheduling architecture](https://developer.hashicorp.com/nomad/docs/concepts/scheduling/schedulers)
  — why batch, service and system workloads use different policies.
- [Nomad advanced scheduling](https://developer.hashicorp.com/nomad/docs/job-scheduling)
  — feasibility, ranking, bin-packing and the placement-quality/latency tradeoff.
- [Kubernetes Scheduling Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
  — scheduling and binding phases, queues and plugin extension points.
- [Modal distributed queues](https://modal.com/docs/guide/queues) — useful as a
  product API example, but note the documented durability limitations.
- [Avoiding insurmountable queue backlogs](https://aws.amazon.com/builders-library/avoiding-insurmountable-queue-backlogs/)
  — backlog age, load shedding and recovery capacity.

Read next:

- [Load shedding in the Nomad evaluation broker](https://www.hashicorp.com/en/blog/load-shedding-in-the-nomad-eval-broker)
  — a production case study about heartbeat storms, millions of evaluations,
  queue pressure and scheduler load.

Optional papers, in increasing depth:

- [Large-scale cluster management at Google with Borg](https://research.google/pubs/large-scale-cluster-management-at-google-with-borg/)
  — the foundation for modern cluster managers.
- [Omega: flexible, scalable schedulers](https://research.google/pubs/omega-flexible-scalable-schedulers-for-large-compute-clusters/)
  — parallel schedulers using shared state and optimistic concurrency.
- [Sparrow: distributed, low-latency scheduling](https://amplab.cs.berkeley.edu/publication/sparrow-distributed-low-latency-scheduling/)
  — decentralized scheduling for very short tasks.
- [Firmament: fast centralized scheduling at scale](https://research.google/pubs/firmament-fast-centralized-cluster-scheduling-at-scale/)
  — evidence that a centralized scheduler can still scale with the right model.

Questions for MiniModal:

- What event creates a scheduling evaluation?
- Is an assignment durable before the agent sees it?
- Can two scheduler replicas create conflicting allocations?
- Does `Claim(node-a)` perform an indexed lookup or scan all historical jobs?
- How are priority, fairness and starvation represented?

### Stage 5 — Reliable distributed execution (5–8 hours)

Goal: handle the fact that a timeout cannot tell you whether the remote action
happened.

Read all of these:

- [Challenges with distributed systems](https://aws.amazon.com/builders-library/challenges-with-distributed-systems/)
- [Making retries safe with idempotent APIs](https://aws.amazon.com/builders-library/making-retries-safe-with-idempotent-APIs/)
- [Timeouts, retries and backoff with jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
- [Exponential backoff and jitter](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/)
- [Leader election in distributed systems](https://aws.amazon.com/builders-library/leader-election-in-distributed-systems/)
- [Instrumenting distributed systems for operational visibility](https://aws.amazon.com/builders-library/instrumenting-distributed-systems-for-operational-visibility/)

Then learn the vocabulary:

- idempotency key;
- at-least-once delivery;
- lease and lease expiry;
- fencing token;
- optimistic concurrency/version column;
- acknowledgement versus completion;
- retryable versus terminal error;
- dead-letter queue;
- heartbeat and failure detector.

Do not begin by implementing Raft in MiniModal. Use PostgreSQL as the durable
authority and transactions/row versions for coordination. Learn consensus so
you understand what the database is providing:

- [Raft visualization and reading guide](https://raft.github.io/)
- [The extended Raft paper](https://raft.github.io/raft.pdf)
- [MIT 6.5840 Distributed Systems](https://pdos.csail.mit.edu/6.824/)
  — use the lectures and papers first; its Go labs are an excellent later project.

### Stage 6 — Sandbox isolation and execution (4–8 hours)

Goal: understand why a scheduler is only one part of a sandbox product.

Start with the isolation choices:

```text
plain process < container/runc < gVisor < Firecracker microVM < full VM

faster/lighter ------------------------------------ stronger isolation boundary
```

Read/watch:

- [Firecracker design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)
  — VMM process, jailer, KVM, device model and threat boundary.
- [Firecracker: secure and fast microVMs](https://www.youtube.com/watch?v=PAEMGa-i2lU)
  — official AWS technical presentation.
- [Firecracker paper](https://www.amazon.science/publications/firecracker-lightweight-virtualization-for-serverless-applications)
  — event path, control path and worker architecture.
- [Introduction to gVisor security](https://gvisor.dev/docs/architecture_guide/intro/)
  and [gVisor security model](https://gvisor.dev/docs/architecture_guide/security/).
- [E2B Runtime architecture](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)
  — inspect its Firecracker, cgroup, network namespace, NBD and proxy sections.
- [Modal: detecting cryptominers with syscall signatures](https://modal.com/blog/catching-cryptominers)
  — a concrete example of operating an untrusted multi-tenant runtime with gVisor.

Watch/build:

- [E2B secure code interpreter workshop](https://ai.engineer/talks/how-to-add-secure-code-interpreting-in-your-ai-app)
  — product integration first, with the Firecracker boundary explained near the end.
- [E2B Embed](https://github.com/e2b-dev/runtime/blob/main/embed/README.md)
  — when you have a spare Linux/KVM machine, run the complete stack locally and
  inspect its processes and network. It is explicitly an evaluation deployment,
  not a production topology.

For MiniModal, begin with a local process executor, then Docker. Do not treat a
normal container as sufficient isolation for hostile public code. Firecracker
or gVisor becomes relevant once untrusted multi-tenant execution is the goal.

### Stage 7 — Cold starts, snapshots, storage and networking (4–7 hours)

Goal: understand the work between “scheduled” and “ready.”

Modal engineering material:

- [Fast, lazy container loading](https://modal.com/blog/jono-containers-talk)
  and the [video version](https://www.youtube.com/watch?v=SlkEW4C2kd4) — OCI image
  layers, lazy filesystems, caching and startup bottlenecks.
- [Memory snapshots: checkpoint/restore for sub-second startup](https://modal.com/blog/mem-snapshots)
  — gVisor snapshots, lazy memory loading and compatibility problems.
- [Cold-start performance guide](https://modal.com/docs/guide/cold-start) — separate
  queueing delay from initialization delay.
- [Why started does not mean ready](https://modal.com/blog/unpacking-sandbox-startup-latency)
  — readiness probes and warm sandbox pools.
- [Directory snapshots and pre-warmed sandboxes](https://modal.com/blog/directory-snapshots-resumable-project-state-for-sandboxes)
  — combining generic warm pools with project-specific files.
- [Modal serverless Servers](https://modal.com/blog/serverless-servers) — routing,
  compute-plane workers, HTTP/2 and graceful draining.
- [WireGuard at Modal](https://modal.com/blog/vprox) — stable egress identity for a
  dynamic multi-cloud worker fleet.

Runtime details:

- [Firecracker snapshot support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)
- [Firecracker snapshot compatibility/versioning](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/versioning.md)
- [Lazy memory restore with userfaultfd](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md)
- [Daytona volumes](https://www.daytona.io/docs/en/volumes/) — persistent data
  backed by object storage and mounted into sandboxes.

Measure these as separate histograms:

```text
submit -> scheduled
scheduled -> agent acknowledged
acknowledged -> runtime started
started -> application ready
ready -> completed
```

### Stage 8 — Autoscaling, warm pools and large scale (4–7 hours)

Goal: connect queue depth and startup latency to desired worker capacity.

Read:

- [Modal scaling guide](https://modal.com/docs/guide/scale) — minimum, maximum and
  buffer containers plus scale-down windows.
- [Modal input concurrency](https://modal.com/docs/guide/concurrent-inputs) — one
  container can process multiple inputs, changing autoscaling math.
- [Knative autoscaling](https://knative.dev/docs/serving/autoscaling/) — request
  concurrency and scale-to-zero.
- [Knative architecture](https://knative.dev/docs/serving/architecture/) — how the
  activator buffers the first request while zero-to-one scaling occurs.
- [OpenFaaS autoscaling](https://docs.openfaas.com/architecture/autoscaling/) —
  concrete formulas for RPS, inflight-capacity, CPU and queue-depth scaling.
- [Modal: scaling to one million concurrent sandboxes](https://modal.com/blog/scaling-to-1-million-concurrent-sandboxes-in-seconds)
  — finish with this again; it will make more sense after the earlier stages.

The initial MiniModal autoscaler should use simple queue-depth math:

```text
desired workers = ceil(runnable jobs / target jobs per worker)
```

Then add a minimum warm pool, maximum fleet size, scale-up rate, scale-down delay
and provisioning timeout. Measure before inventing a smarter policy.

## Company-by-company reference library

### Modal

Modal is the best source for extreme-scale and cold-start engineering. Its core
platform is proprietary, so use its engineering posts for design ideas rather
than as source-code reference.

Product docs:

- [Documentation home](https://modal.com/docs/guide)
- [Functions](https://modal.com/docs/guide/functions)
- [Sandboxes](https://modal.com/docs/guide/sandboxes)
- [Scaling](https://modal.com/docs/guide/scale)
- [Input concurrency](https://modal.com/docs/guide/concurrent-inputs)
- [Cold starts](https://modal.com/docs/guide/cold-start)
- [Memory snapshots](https://modal.com/docs/guide/memory-snapshots)
- [Queues](https://modal.com/docs/guide/queues)
- [GPU acceleration](https://modal.com/docs/guide/gpu)
- [Multi-node clusters and gang scheduling](https://modal.com/docs/guide/multi-node-training)
- [Servers](https://modal.com/docs/guide/servers)

Engineering posts:

- [Modal blog index](https://modal.com/blog) — use the Engineering filter for
  new platform internals after this roadmap's review date.
- [Scaling to one million sandboxes](https://modal.com/blog/scaling-to-1-million-concurrent-sandboxes-in-seconds)
- [Fast, lazy container loading](https://modal.com/blog/jono-containers-talk)
- [Memory snapshots](https://modal.com/blog/mem-snapshots)
- [Sandbox startup latency](https://modal.com/blog/unpacking-sandbox-startup-latency)
- [Directory snapshots](https://modal.com/blog/directory-snapshots-resumable-project-state-for-sandboxes)
- [Serverless Servers](https://modal.com/blog/serverless-servers)
- [Static IPs with WireGuard](https://modal.com/blog/vprox)
- [Cryptominer detection in gVisor](https://modal.com/blog/catching-cryptominers)
- [Modal is a computer](https://modal.com/blog/modal-is-a-computer)
- [Sandbox launch and use cases](https://modal.com/blog/sandbox-launch)

Videos/interviews:

- [Why and how we built Modal](https://www.youtube.com/watch?v=qeDPKbWjsuk)
- [Fast, lazy container loading](https://www.youtube.com/watch?v=SlkEW4C2kd4)
- [Building Modal's infrastructure from scratch](https://www.youtube.com/watch?v=pLBxrY8RX6w)
- [The future of AI infrastructure](https://www.youtube.com/watch?v=UwxxlTNPjWo)

### Daytona

Daytona is the best product model for long-lived agent computers: lifecycle,
resizing, snapshots, volumes, runners and direct access to sandbox services.

Docs:

- [Documentation home](https://www.daytona.io/docs/)
- [Guides index](https://www.daytona.io/docs/en/guides/)
- [Architecture](https://www.daytona.io/docs/en/architecture/)
- [Sandboxes and lifecycle](https://www.daytona.io/docs/sandboxes)
- [Scaling](https://www.daytona.io/docs/en/scale/)
- [Volumes](https://www.daytona.io/docs/en/volumes/)
- [Limits and shared resource pools](https://www.daytona.io/docs/limits)
- [API reference](https://www.daytona.io/docs/tools/api/)
- [Changelog](https://www.daytona.io/changelog) — useful for following changes
  to runners, warm pools, snapshots, GPUs, recovery and observability.
- [Job-based runner architecture announcement](https://www.daytona.io/changelog/job-based-runners-custom-regions)

Source and video:

- [Daytona source repository](https://github.com/daytonaio/daytona) — valuable
  historically, but the repository states that core development moved private
  in June 2026. Use current docs as the authority for current behavior.
- [Giving Agents Computers](https://www.youtube.com/watch?v=kaX43RRRUKY)

### E2B

E2B is the best implementation reference for this project because its current
runtime is open source, mostly Go, and explicitly separates control and data planes.

Read in this order:

- [Runtime overview](https://github.com/e2b-dev/runtime)
- [Full architecture](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)
- [Repository layout and local development](https://github.com/e2b-dev/runtime/blob/main/DEV-LOCAL.md)
- [Single-machine E2B Embed](https://github.com/e2b-dev/runtime/blob/main/embed/README.md)
- [E2B SDK/product documentation](https://e2b.dev/docs)
- [E2B open-source project map](https://e2b.dev/open-source)
- [Security and microVM isolation](https://e2b.dev/security)
- [Main SDK and examples repository](https://github.com/e2b-dev/E2B)

Watch:

- [How to add secure code interpreting to an AI app](https://ai.engineer/talks/how-to-add-secure-code-interpreting-in-your-ai-app)
- [Firecracker technical presentation](https://www.youtube.com/watch?v=PAEMGa-i2lU)

Source-reading route through `e2b-dev/runtime`:

1. Read `docs/ARCHITECTURE.md` without opening implementation files.
2. Read protobuf/OpenAPI contracts to learn the boundaries.
3. Follow one sandbox-create request through API to orchestrator.
4. Follow one process-execution request through proxy to `envd`.
5. Follow pause/resume and identify what is persisted to object storage.
6. Inspect database migrations and queries only after understanding the flow.
7. Compare each component with the equivalent package you expect in MiniModal.

### Similar systems worth studying

- [HashiCorp Nomad](https://developer.hashicorp.com/nomad/docs/architecture) —
  the clearest general-purpose scheduler/client architecture.
- [Kubernetes controllers](https://kubernetes.io/docs/concepts/architecture/controller/)
  and [scheduler framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
  — desired-state reconciliation and extensible placement.
- [Knative Serving](https://knative.dev/docs/serving/architecture/) — queueing and
  scale-to-zero request routing on Kubernetes.
- [OpenFaaS](https://docs.openfaas.com/architecture/stack/) — an approachable
  function platform with a gateway, queue worker, autoscaler and containers.
- [Jamsocket Plane](https://github.com/jamsocket/plane) — an open-source
  orchestrator for session-lived processes and isolated code environments;
  conceptually useful for long-lived sandboxes.
- [Firecracker](https://github.com/firecracker-microvm/firecracker) — the microVM
  runtime used by E2B and AWS Lambda/Fargate.
- [gVisor](https://gvisor.dev/docs/architecture_guide/intro/) — the application
  kernel used by Modal for stronger-than-runc container isolation.

## Distributed-systems foundation

Use one lecture series as the backbone rather than watching random system-design
videos:

- [Martin Kleppmann's Cambridge lectures](https://www.youtube.com/playlist?list=PLeKd45zvjcDFUEv_ohr_HdUFe97RItdiB)
  — approachable and directly relevant.
- [MIT 6.5840](https://pdos.csail.mit.edu/6.824/) — deeper and more
  implementation-heavy; return to it when MiniModal reaches replication and HA.
- *Designing Data-Intensive Applications*, second edition, by Martin Kleppmann
  and Chris Riccomini — use the book for durable state, replication, partitions,
  transactions, streams and batch processing. It is not required before coding.

Recommended concept order:

1. RPC and serialization.
2. Partial failure and timeouts.
3. Idempotency and retries.
4. Clocks, ordering and logical time.
5. Replication and consistency.
6. Transactions and optimistic concurrency.
7. Leases, failure detectors and fencing.
8. Leader election and consensus.
9. Partitioning and horizontally scalable control planes.

## What to observe while reading

For every system, answer the same questions. These questions turn passive
reading into architecture practice.

### API and durability

- What is the public API object: invocation, job, function, sandbox or machine?
- What operation is synchronous, and what merely means “accepted”?
- What database is authoritative?
- Are API writes idempotent?
- Can state be reconstructed after a restart?

### Scheduling

- What causes a scheduling pass?
- Is scheduling centralized, partitioned or decentralized?
- How are candidate nodes indexed and filtered?
- When is capacity reserved?
- How are concurrent scheduling conflicts detected?
- What happens to pending and permanently impossible workloads?

### Agent protocol

- Does the control plane push, stream, long-poll or short-poll?
- Who initiates the network connection?
- How does a node register capacity and capabilities?
- What is acknowledged, and when?
- What lease/fencing mechanism prevents two agents from running one assignment?

### Runtime

- Process, container, gVisor or microVM?
- How are CPU, memory, disk and network enforced?
- How does the host reach the sandbox, and how does the sandbox reach the internet?
- Where do stdout, stderr, metrics and exit status go?
- What happens if the node dies without cleanup?

### Scaling and operations

- What is kept warm?
- Which metric drives scaling: queue depth, concurrency, CPU or latency?
- How is zero-to-one traffic buffered?
- How are images, model weights and snapshots cached?
- Which metrics expose queue age, scheduling latency and startup latency?

## Suggested MiniModal implementation order

Use the material at the moment it becomes concrete in the project:

1. Public HTTP API and typed errors.
2. Separate `controlplane` and `agent` binaries.
3. PostgreSQL job, node and assignment records.
4. Idempotent submission and versioned state transitions.
5. Agent registration, heartbeat and capacity reporting.
6. Blocking claim or bidirectional gRPC stream.
7. Assignment acknowledgement, lease expiry and retry.
8. Local-process executor with timeout, cancellation, stdout and exit code.
9. Docker executor with CPU/memory constraints.
10. Metrics plus a deterministic load/failure simulator.
11. Warm worker pool and queue-depth autoscaler.
12. Multiple control-plane replicas using the database for coordination.
13. Stronger sandbox isolation with gVisor or Firecracker on Linux/KVM.
14. Optional GPU inventory, device assignment and gang scheduling.

At stages 1–10, PostgreSQL can provide the durable coordination needed to learn
the real distributed-system behavior. A custom replicated log or consensus
implementation is not required to make MiniModal genuine.

## A practical study routine

For each 45–90 minute study session:

1. Read or watch one core resource.
2. Draw its components without looking at the source.
3. Trace one request from client to sandbox and back.
4. Write down one failure at every network arrow.
5. Compare the flow with MiniModal's current packages.
6. Add only the next concept to MiniModal; do not copy the whole architecture.

A useful notebook format is:

```text
resource:
main idea:
control-plane components:
data-plane components:
source of truth:
normal request path:
three failure cases:
what MiniModal should adopt now:
what MiniModal should postpone:
```

## Current scope boundary

For the next milestones, focus on CPU-only Linux sandboxes. GPU scheduling is a
later specialization. The control plane, persistence, RPC, leases, retries,
heartbeats, sandbox lifecycle, logs and autoscaling all transfer directly when
GPUs are introduced. GPU work mainly adds heterogeneous device inventory,
topology, fragmentation, costly warm capacity and gang scheduling.
