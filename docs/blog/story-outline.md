# From a function call to a running worker

Writing notes, not a draft. Keep one invocation visible through every section. The throughline: **accepting work, placing work, creating capacity, and executing work are different promises**. PostgreSQL connects them; a wakeup channel does not.

## Opening: the deceptively small API

- Start with `add.spawn(1, 2)` or `add.remote(1, 2)`. What does the caller believe happened when the HTTP request returns?
- Show the gap between an accepted job and a running function. This is the question the rest of the piece answers.
- Scope it honestly: Go control plane, Python functions, Docker executor, local process provisioner. No claim of production-grade sandboxing.
- Visual: [invocation path](assets/invocation-path.svg). It should reveal admission first, execution second.

## HLD: one call, several independent loops

- Show SDK/UI → HTTP control plane → PostgreSQL; scheduler reads pending work; capacity manager writes desired nodes; provisioner starts/drains agents; agents claim over gRPC, fetch artifacts from S3, and run code.
- Distinguish durable state (jobs, versions, leases, desired node counts) from process-local hints and observations (wakeup, heartbeats, process handles).
- Point out that these modules currently share one control-plane process. The boundaries are conceptual, not separate deployed services.
- Visual: [system map](assets/system-map.svg) ([edit in Excalidraw](assets/system-map.excalidraw)), then [animated walkthrough](visuals/system-walkthrough.html). Reveal one responsibility at a time, reusing the same layout.
- Source: [HLD and LLD index](../architecture/README.md).

## Submission: make intent durable before returning

- Immutable function version + artifact key; the job records the invocation and version ID. What belongs to the version, and what belongs to an invocation?
- Commit the job and event; return before execution. Repeated equivalent job IDs are idempotent; conflicting specifications are rejected.
- Non-blocking wakeup only prompts a scheduler pass. The pending row is the recoverable queue entry; the periodic pass covers missed hints.
- Learning beat: asynchronous admission decouples client latency from placement but does not create infinite throughput.
- Source: [HTTP/API](../architecture/http-api.md), [control plane](../architecture/control-plane.md).

## Scheduler: choose a node, not a machine

- Read due pending jobs in priority order, in bounded batches. Explain the cursor: an unplaceable head of queue must not starve later jobs.
- Rebuild available resources from healthy nodes and active assignments; first-fit produces reservations and leases.
- Separate “cannot fit now” from “cannot fit any known node.” The scheduler does not launch agents.
- Learning beat: priority, fairness, and bounded work are separate decisions. This is a simple placement policy, not a fair-share scheduler.
- Visual: one job moving through `pending → assigned`, with two candidates and a resource fit check.
- Source: [scheduler LLD](../architecture/scheduler.md).

## Capacity manager and provisioner: desired state is not observed state

- Capacity manager compares waiting work, existing free slots, and hypothetical nodes from a pool. Its output is `node_pools.desired_nodes` in PostgreSQL.
- Provisioner reconciles that desired count with locally owned agent processes; agents register and heartbeat through the normal gRPC path.
- Scale-down is drain, not immediate kill: stop new claims, let active attempts finish, then exit. An unhealthy node and a deliberately draining node are different cases.
- Learning beat: planning capacity and creating capacity must be independent of placing individual jobs.
- Visual: [desired-versus-observed loop](assets/capacity-loop.svg) ([edit in Excalidraw](assets/capacity-loop.excalidraw)); use its moving arrows while explaining scale-up, registration, and drain.
- Source: [capacity manager](../architecture/capacity-manager.md), [provisioner](../architecture/provisioner.md).

## Agent, leases, and failure: execution is an attempt

- Claim an assignment; download the version archive; execute Python; stream logs; renew the lease; report a result.
- Attempt number fences a late result after re-claim. Expired leases can produce another attempt: at-least-once, not exactly-once.
- Cancellation reaches the worker through renewal; context cancellation stops the executor, but the job state still needs an acknowledgement.
- Learning beat: the hardest boundary is between a side effect in the container and the database transaction recording completion.
- Visual: two overlapping attempts; the older one finishes late and is rejected.
- Source: [agent and gRPC](../architecture/agent-and-grpc.md), [execution](../architecture/execution-and-artifacts.md).

## PostgreSQL: shared state, not a magic queue

- Job rows and transactions make submission, assignment, retry, and completion recoverable; S3 holds archives, not job state.
- Explain why moving a wakeup channel or heartbeats into Redis would not, by itself, make the control plane horizontally scalable.
- Tradeoff: a shared database simplifies the prototype, but the scheduler still has a process-local mutex and heartbeat view, and large scans are real costs.
- Learning beat: separate source of truth, notification, and observation. Name each before choosing infrastructure.
- Source: [persistence LLD](../architecture/persistence.md).

## A small measurement, not the headline

- One 100,000-job paced hello-world run with an isolated load generator and host-process executor (not Docker per job): 249.9 submissions/s delivered, 246.0 full-run completions/s, all succeeded. The app/agents had a 4-CPU/6-GiB limit; PostgreSQL had a separate 4-CPU/4-GiB limit.
- The 275/s run built a backlog and had much worse tail latency. One run at each rate does **not** establish a precise sustainable ceiling. Both runs used dirty code; the Mac host was shared.
- What this taught: fast admission can hide slow scheduling/claiming; the first unbounded pending scan was a useful failure, not a throughput claim.
- Keep numbers to one callout or short paragraph. Full method, resource traces, and caveats belong in the [benchmark report](../benchmark-hello-process-2026-10-03.md).

## Ending: what the prototype does not solve

- Local agents are OS processes, not provisioned VMs; Docker is not a security boundary for untrusted tenants.
- One control-plane instance; no shared heartbeat state, multi-scheduler coordination, quotas, or fair-share policy.
- Close with the design lesson, not a scale boast: each loop should make one durable transition and be able to explain what happens when it stops.

## Visual production notes

- Keep the system map fixed and highlight only the actors relevant to the current paragraph. Do not redraw a different architecture in each section.
- Open the `.excalidraw` files in Excalidraw to move boxes, arrows, and labels. The SVGs are publishable exports; after editing a scene, export a fresh SVG from Excalidraw. The HTML walkthrough is a separate, step-controlled animation and does not automatically sync with Excalidraw edits.
- For a platform that strips scripts or SVG animation, export a short screen recording and use the static SVG as the fallback.
- Use explicit state labels (`pending`, `assigned`, `running`) and distinguish **desired** nodes from **observed** agents. No moving arrows without a state change to explain.
- Reference structure: [Devanshu's sandbox design](https://medium.com/@devanshusharma658/agent-sandboxes-at-scale-a-distributed-systems-design-for-ai-driven-development-3765f4a9460c) moves from the user problem to HLD and component responsibilities; [PlanetScale's architecture of Neki](https://planetscale.com/blog/the-architecture-of-neki) keeps a stable diagram and highlights components; [massively parallel Postgres backups](https://planetscale.com/blog/massively-parallel-postgres-backups) reveals one lifecycle step at a time. Use the pacing, not their prose or illustrations.
