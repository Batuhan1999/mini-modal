# mini-modal

A small, self-hosted take on [Modal](https://modal.com) function invocation workflow.
You decorate a function with `@app.function()`, at at runtime this code will run on a seperate machine. 

Goal: 
- Implement distributed systems techniques
- Learn GO
- Learn perfomance characteristics. 

I built it to learn Go and the basics of distributed systems. It runs on one
machine and is not meant for real workloads. There is no authentication, only
one control-plane process, and Docker is not a security boundary for untrusted
code.

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


## The Plan
![mini-modal architecture](docs/blog/assets/architecture.png)


Python Sdk: 
Just a small sdk that allows us to upload the decorated function. Then you can call the function with .spawn and .get. 
Basically mimics modals sdk but with a little twist. What modal does is that they package every file in where ypu have a decorated function and deploy it even if the code doesnt have to do with the decorated function.

Example

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

The problem that arises here is that .deploy is triggered even if you change anything not related to your function even like function invocations because the sdk hast the contents of the artifact.

So my idea was to use a AST and tree shaking to only upload the parts that are neccessary for the invocation. Because of dynmaic import we still need a way to include some modules ourselves. 

![What app.deploy() uploads for greet](docs/blog/assets/packaging.png)

![How app.deploy() decides whether to upload](docs/blog/assets/deploy-cache.png)


This has the effect that unneccassary deploys are reduced which are big problem because if you are developing the application a 2s addional reload time reduces the DX drastically.


Control Plane: 
A simple HTTP endpoint that handles the the lifecyle events of a function(deployment,submission, cancellation, status reports etc) plsu functions versioning.

-Function structure
(<<-claude maybe we should explaind the data structure before we even go into the plan part ?)
<<claude, show data structure here function, fucntion_Version, job>

- Whats a function:
  <<claude:a job show the simple struct that we have.>
  This is the unique identificaiton to every decorated function that you have in our simple case its the name of the function 

- Whats a function_version:
  <<claude:a job show the simple struct that we have.>
  This allows us to save multiple versions of a function if sth changes. Here is also save the requirement of the invocation. 
  like how much ram, cpu or gpus it needs.

- Whats a job:
  <<claude:a job show the simple struct that we have.>
  this is execution that will run on of our agents.

So the controlplane will validate the job nd put it into our shared db with the status pending so that it can be picked up by our scheduler

Scheduler:
For this case a simple First-Fit-placement scheduler. It takes pending jobs in pending and checks the current avaialabe nodes and tries to place this job on it. 
<<claude diagram to explain this>>


Capacity manager.
The capacity managers job is it to check the current demand (the assigned + pending jobs) and compare it with the current resources
and ensure that to either start a new agent that can run this job. Because we want to have a small leeway we also keep a buffer of warm nodes so we can handle spikes
<<claude diagram to explain this>>


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


