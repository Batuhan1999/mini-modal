# Python SDK — LLD

**Code:** `sdk/python/mini_modal/`, `sdk/python/tests/`.

`Client` wraps the HTTP API. `App.function` registers a Python callable and
its runtime settings locally; a plain call still executes locally. `App.deploy`
packages registered functions, uploads changed artifacts, creates or reuses
versions, and binds version IDs to the decorators. `spawn()` submits an
invocation and returns a job handle; `remote()` waits for the result. The
handle can fetch status, result, logs, and events or request cancellation.

Packaging parses the source AST of each module-level decorated function,
includes reachable definitions from its file, follows static local imports,
and preserves package paths. Dynamic imports must be declared with
`include_modules`. Third-party packages are not bundled; they belong in the
worker image. Arguments and return values must be JSON-compatible.

The archive digest plus effective settings form a deployment fingerprint.
An unchanged artifact can reuse the remote version. A local cache in
`.mini-modal/deployments/` can bind a known version without a network request
on process restart. It is disposable; `deploy(force=True)` asks the control
plane again. If the database was reset, an invocation can refresh a cached
version once. The cache does not deploy a function that was never deployed.

The AST reduction is a pragmatic static analysis, not a complete Python
dependency resolver. Runtime import machinery, reflection, and environment
differences may require explicit includes or a custom image. See the
[SDK usage guide](../../sdk/python/README.md) for runnable examples.
