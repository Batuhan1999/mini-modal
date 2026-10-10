# mini-modal (distributed job scheduler)

A small, self-hosted take on [Modal](https://modal.com)'s function invocation workflow.
You decorate a function with `@app.function()`, and at runtime this code will run on a separate machine.

Goals:
- Implement distributed systems techniques
- Learn Go
- Learn performance characteristics

It runs on one machine and is not meant for real workloads. There is no
authentication, only one control-plane process, and Docker is not a security
boundary for untrusted code.

```python
from mini_modal import App

app = App("demo")

@app.function(cpus=1, memory_gb=1)
def greet(name: str) -> str:
    return f"Hello, {name}!"

if __name__ == "__main__":
    app.deploy()
    call = greet.spawn("Ada")     # returns as soon as the job is stored
    print(call.get())             # waits for the result
    print(greet.remote("Grace"))  # spawn and wait in one call
```

## Supported features

**Deploying functions**
- Uploading an artifact
- Tree-shaken artifacts and skipping unchanged deploys
- Immutable function versions

**Jobs**
- Idempotent job submission
- Job priorities
- Job timeouts
- Cancellation of a job

**Scheduling and capacity**
- First-fit scheduling
- Capacity management and provisioning on backlog
- Node registration and heartbeats
- Graceful node draining

**Execution**
- Docker execution with CPU and memory limits
- Live log streaming from the node, stored per attempt

**Failure handling**
- Leases and fencing tokens
- Retrying with a fixed backoff

**Observability and tooling**
- Job lifecycle event history
- Prometheus metrics and a Grafana dashboard
- Web dashboard for deploying, invoking, and inspecting jobs
- Load simulator and benchmark harness

## The Plan

![mini-modal architecture](docs/blog/assets/architecture.png)

### Function structure

![Function, function version, and job](docs/blog/assets/data-model.png)

- What's a function:
  ```go
  // internal/store/postgres/sqlc/models.go
  type Function struct {
      ID        string // "demo.greet"
      CreatedAt int64
  }
  ```
  This is the unique identifier for every decorated function you have. In our simple case, it's the app name plus the function name, like `demo.greet`.

- What's a function_version:
  ```go
  type FunctionVersion struct {
      ID                  string
      FunctionID          string // "demo.greet"
      ArtifactKey         string // ".../artifacts/<sha256>.tar.gz" in S3
      Image               string // "mini-modal-python:dev"
      GpuType             string
      Gpus                int64
      Cpus                int64
      MemoryGb            int64
      MaxAttempts         int64
      RetryBackoffSeconds int64
      CreatedAt           int64
      Entrypoint          string // "demo.greet:greet"
      TimeoutSeconds      int64
  }
  ```
  This allows us to save multiple versions of a function if something changes. It also stores the requirements of the invocation, like how much RAM, CPU, or GPUs it needs.

- What's a job:
  ```go
  type Job struct {
      ID                string
      Priority          int64
      Status            string          // pending, assigned, running, succeeded, ...
      NodeID            string          // set by the scheduler
      Attempt           int64           // +1 on every assignment
      LeaseExpiresAt    int64           // the agent must claim or renew before this
      UpdatedAt         int64
      NextAttemptAt     int64           // retry backoff
      LastError         string
      ExecutionAttempts int64           // +1 on every claim
      FunctionVersionID string          // which version to run
      InputJson         json.RawMessage // {"args": ["Ada"], "kwargs": {}}
      CreatedAt         int64
  }
  ```
  This is an execution that will run on one of our agents.

### Python SDK

Just a small SDK that allows us to upload the decorated function. Then you can call the function with `.spawn()` and `.get()`.
It basically mimics Modal's SDK, but with a little twist. What Modal does is package every file where you have a decorated function and deploy it, even if the code doesn't have anything to do with the decorated function.

Example:

```python
# demo/app.py, deployed with: modal deploy -m demo.app
import modal

from demo import report
from demo.text_utils import title_case

app = modal.App("demo")

GREETING = "Hello"


@app.function()
def greet(name: str) -> str:
    return f"{GREETING}, {title_case(name)}!"


def print_weekly_report():
    print(report.build())


@app.local_entrypoint()
def main():
    print(greet.remote("ada lovelace"))
```

What ends up in the container:

```text
demo/              the whole package, because greet is defined in it
├── __init__.py
├── app.py         all of it, including print_weekly_report and main
├── report.py      greet never uses this
└── text_utils.py
```

The problem that arises here is that `.deploy()` is triggered even if you change anything not related to your function, even function invocations, because the SDK hashes the contents of the artifact.

So my idea was to use an AST and tree shaking to only upload the parts that are necessary for the invocation. Because of dynamic imports, we still need a way to include some modules ourselves (`include_modules`).

![What app.deploy() uploads for greet](docs/blog/assets/packaging.png)

![How app.deploy() decides whether to upload](docs/blog/assets/deploy-cache.png)

This has the effect that unnecessary deploys are reduced. They are a big problem, because when you are developing the application, an additional 2 s of reload time hurts the DX drastically.

### Control plane

A simple HTTP API that handles the lifecycle events of a function (deployment, submission, cancellation, status reports, etc.) plus function versioning.

So the control plane will validate the job and put it into our shared DB with the status `pending`, so that it can be picked up by our scheduler.

### Scheduler

For this case, a simple first-fit placement scheduler. It takes the pending jobs, checks the currently available nodes, and tries to place each job on one of them.

![First-fit scheduling pass](docs/blog/assets/scheduler.png)

### Capacity manager

The capacity manager's job is to check the current demand (the pending jobs), compare it with the free resources, and calculate the target state of resources. Because we want to have a small leeway, a next step would be to also keep a buffer of warm nodes so we can handle spikes.

![Capacity planning pass](docs/blog/assets/capacity.png)

### Provisioning

This service's job is to fulfill the target state that the capacity manager planned. It starts the nodes in each node pool necessary to meet the target state. In this example, it just runs the agent binary as a child process. In a production system, this would be replaced with bare-metal servers.

### Node (data plane)

The actual service that is responsible for taking the assigned jobs and running them. For the local case, I just spawn Docker containers, but in production they would be replaced with microVMs (with snapshots) to improve latency.

## Benchmarks

In one run, with the control plane and agents limited to 4 CPUs and
PostgreSQL in its own container, it completed 100,000 hello-world calls
submitted at 250 per second. That run executed functions as host processes,
not Docker containers, on a shared Mac. It is one data point, not a capacity
claim. The [report](docs/benchmark-hello-process-2026-10-03.md) covers the
setup, the runs that did not finish, and what changed between them. The
[methodology](docs/benchmarking.md) explains how to reproduce it, and raw
results are [indexed here](benchmarks/results/README.md).

## Limits

- One control-plane process keeps node heartbeats in memory. Running several
  replicas would need shared health state and coordination.
- The provisioner starts agent processes on the same machine. It stands in for
  a VM provisioner.
- Functions run in plain Docker containers, not microVMs. GPU requests affect
  placement, but no real GPUs are attached.
- Scheduling and capacity planning scan PostgreSQL rows directly. There are no
  quotas, fair-share scheduling, or warm pools.
- PostgreSQL is the only supported database. Older benchmark results used
  SQLite.
