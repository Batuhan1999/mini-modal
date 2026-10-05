# Execution and artifacts — LLD

**Code:** `internal/executor/`, `internal/artifact/`, `sdk/python/Dockerfile`.

The executor interface takes a context, assignment, and stdout/stderr/system
writers, and returns a JSON value or error. Three adapters exist: `Fake`
waits two seconds for tests and load experiments; `ProcessExecutor` runs
Python on the agent host; `DockerExecutor` runs a Python image with CPU and
memory limits. Production-like local runs use Docker, not the fake executor.

An artifact is a `.tar.gz` archive stored under a function-specific,
SHA-256-derived S3 key. Upload rejects archives over 32 MiB compressed or
256 MiB uncompressed and disallows unsafe paths and non-regular entries.
Deploy verifies that the key belongs to the function and exists. At runtime,
the agent downloads and extracts the archive into a temporary directory.

Docker execution checks/pulls the image, mounts the archive read-only at
`/workspace`, mounts a separate writable result directory, passes JSON input
on stdin, and runs the version's `module:function` entrypoint with the Python
runner. It reads `result.json` only after a successful run. Temporary host
directories and the named container are cleaned up. Context cancellation
terminates the Docker CLI command and cleanup forcibly removes its container.

The image must contain Python and libraries imported by uploaded code. The
SDK's default image includes `mini_modal`. The archive validator protects
extraction paths, but Docker here is not a security boundary for untrusted
tenants: there is no network policy, hardened runtime, or real GPU device
assignment. The process executor has no CPU/memory isolation.
