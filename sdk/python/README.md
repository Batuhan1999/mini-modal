# mini-modal Python client

Install from the repository root with `python3 -m pip install -e sdk/python`.
The control plane, an agent, and object storage must already be running.
Build the default worker image once on the Docker host used by your agent:

```sh
docker build -t mini-modal-python:dev sdk/python
docker run --rm mini-modal-python:dev python -c "import mini_modal"
```

```python
from mini_modal import Client

client = Client("http://localhost:8080")
greet = client.deploy_python_file(
    "greet",
    "hello_function.py",
    "greet",
    cpus=1,
    memory_gb=1,
)

call = greet.spawn("Ada")
print(call.job_id, call.status()["status"])
print(call.get(timeout=300))

# Or wait in one call:
print(greet.remote(name="Ada"))

# Reuse an already deployed version without uploading again:
same_greet = client.function("greet", greet.version_id)
```

`spawn()` returns a job handle immediately. `remote()` waits for the function
result. `call.get()` polls until success, failure, cancellation, or its timeout;
its timeout does not cancel the job. The handle also supports `status()`,
`cancel()`, `logs()`, and `events()`.

Failed calls raise `FunctionFailedError`. Its message uses the final stderr
line when available, so a Python exception is visible without opening the
logs. The exception also exposes `job_id`, `last_error` (the control-plane
error), and `stderr` (up to the last 8 KiB). If logs cannot be fetched, the
message falls back to `last_error`.

`@app.function(...)` now registers a Python function and its version settings
without contacting the control plane:

```python
from mini_modal import App

app = App("demo")

@app.function(cpus=1, memory_gb=1)
def greet(name: str) -> str:
    return f"Hello, {name}!"

print(greet("Ada"))  # Normal local Python call.
print(app.functions["greet"].id)  # demo.greet

if __name__ == "__main__":
    app.deploy()
    print(greet.remote("Ada"))
```

The decorator only registers the function locally. `app.deploy()` uploads the
Python source file, creates a version for each registered function, and binds
its version ID so `spawn()` and `remote()` can invoke it. Registered functions
must be defined at module level in a `.py` file. Deployment prints progress
for each function. If the uploaded artifact and all version settings match an
existing version, it reuses that version ID; otherwise it creates a new one.
It still uploads the source to check its content-addressed artifact key.
Keep the deployment call under `if __name__ == "__main__"` so importing the
module in a worker does not deploy it again. Both `@app.function()` and
`deploy_python_file()` use `mini-modal-python:dev` by default. You can still
pass `image=` to override it; a custom image must also contain the SDK if its
uploaded source imports `mini_modal`. Build the default image on every agent's
Docker host, or publish it to a registry those hosts can pull from.

`deploy_python_file()` uploads one Python file and creates an immutable
function version. For multi-file artifacts, upload a `.tar.gz` archive with
`client.upload_function()` and then create a version with
`client.deploy_function()`. The arguments and return value must be JSON-compatible,
and the runtime image must contain any libraries imported by your function.
