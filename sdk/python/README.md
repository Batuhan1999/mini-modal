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

The decorator only registers the function locally. `app.deploy()` creates a
function-specific archive containing the function, its referenced definitions
from the same file, and local Python modules imported by those sources. It
follows imports in local modules transitively and preserves package paths.
Registered functions must be defined at module level in a `.py` file. The
archive's content hash determines whether it needs uploading; an unchanged
artifact and version settings reuse the existing version ID. Deployment prints
the upload and version status for each function.

`app.deploy()` also stores a local fingerprint and version ID in
`.mini-modal/deployments/` beside the source package. On a later process start,
it rebuilds the function artifact and fingerprints its contents, entrypoint,
and effective settings. If these match the local record, it binds the known
version without contacting the control plane. Different control-plane URLs have
separate records. The cache is generated and safe to delete; use
`app.deploy(force=True)` to check the control plane explicitly. If a cached
version is gone after a database reset, the decorated function's first
`spawn()` refreshes the deployment and retries once. Set
`App(..., cache_dir=path)` to place the cache elsewhere. This does not make
`spawn()` deploy a never-deployed function:
call `app.deploy()` at least once before invoking it.

Imports constructed at runtime cannot always be discovered from source. Include
their local module or package explicitly; a package includes its `.py` files:

```python
@app.function(include_modules=("plugins",))
def run_plugin(name):
    import importlib
    return importlib.import_module("plugins." + name).run()
```

`include_modules` resolves names from the source file's import root. A normal
Python package (with `__init__.py`) keeps its dotted module path in the worker.
Third-party libraries still need to be installed in the worker image.

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
