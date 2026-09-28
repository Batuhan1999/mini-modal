"""Small Python client for the mini-modal control-plane API."""

from __future__ import annotations

import inspect
import sys
import time
from dataclasses import asdict, dataclass
from functools import update_wrapper
from pathlib import Path
from typing import Any, Callable
from urllib.parse import quote
from uuid import uuid4

import requests

__all__ = ["App", "Client", "Function", "FunctionFailedError", "JobHandle"]

_WAITING_STATUSES = {
    "pending", "assigned", "running", "retrying", "cancelling", "unschedulable"
}
_MAX_ARTIFACT_BYTES = 32 << 20
_DEFAULT_PYTHON_IMAGE = "mini-modal-python:dev"
_DEFAULT_MAX_ATTEMPTS = 3
_DEFAULT_RETRY_BACKOFF_SECONDS = 1


class FunctionFailedError(RuntimeError):
    def __init__(self, job_id: str, last_error: str, stderr: str = ""):
        self.job_id = job_id
        self.last_error = last_error
        self.stderr = stderr
        detail = stderr.strip().splitlines()[-1] if stderr.strip() else last_error or "unknown error"
        super().__init__(f"job {job_id!r} failed: {detail}")


class Client:
    def __init__(self, base_url: str = "http://localhost:8080"):
        self.base_url = base_url.rstrip("/")

    def function(self, function_id: str, version_id: str) -> Function:
        return Function(function_id, self, version_id)

    def list_functions(self) -> list[dict[str, Any]]:
        response = requests.get(f"{self.base_url}/v1/functions", timeout=10)
        response.raise_for_status()
        return response.json()

    def get_function_versions(self, function_id: str) -> list[dict[str, Any]]:
        response = requests.get(
            f"{self.base_url}/v1/functions/{quote(function_id, safe='')}/versions",
            timeout=10,
        )
        response.raise_for_status()
        return response.json()

    def get_function_version(self, version_id: str) -> dict[str, Any]:
        response = requests.get(
            f"{self.base_url}/v1/function-versions/{quote(version_id, safe='')}",
            timeout=10,
        )
        response.raise_for_status()
        return response.json()

    def upload_function(self, function_id: str, source_path: str | Path) -> dict[str, Any]:
        path = Path(source_path)
        if path.suffix == ".py":
            content_type = "text/x-python"
            params = {"filename": path.name}
        elif path.name.endswith(".tar.gz"):
            content_type = "application/gzip"
            params = None
        else:
            raise ValueError("artifact must be a .py file or .tar.gz archive")

        with path.open("rb") as source:
            data = source.read(_MAX_ARTIFACT_BYTES + 1)
        if not data or len(data) > _MAX_ARTIFACT_BYTES:
            raise ValueError("artifact must be between 1 byte and 32 MiB")

        response = requests.post(
            f"{self.base_url}/v1/functions/{quote(function_id, safe='')}/artifacts",
            params=params,
            data=data,
            headers={"Content-Type": content_type},
            timeout=30,
        )
        response.raise_for_status()
        return response.json()

    def deploy_function(
        self,
        function_id: str,
        *,
        artifact_key: str,
        image: str,
        entrypoint: str,
        cpus: int = 1,
        memory_gb: int = 1,
        gpu_type: str = "",
        gpus: int = 0,
        timeout_seconds: int = 300,
        max_attempts: int = 1,
        retry_backoff_seconds: int = 0,
    ) -> dict[str, Any]:
        response = requests.post(
            f"{self.base_url}/v1/functions/{quote(function_id, safe='')}/versions",
            json={
                "artifact_key": artifact_key,
                "image": image,
                "entrypoint": entrypoint,
                "cpus": cpus,
                "memory_gb": memory_gb,
                "gpu_type": gpu_type,
                "gpus": gpus,
                "timeout_seconds": timeout_seconds,
                "max_attempts": max_attempts,
                "retry_backoff_seconds": retry_backoff_seconds,
            },
            timeout=30,
        )
        response.raise_for_status()
        return response.json()

    def deploy_python_file(
        self,
        function_id: str,
        source_path: str | Path,
        function_name: str,
        *,
        image: str = _DEFAULT_PYTHON_IMAGE,
        **version_settings: Any,
    ) -> Function:
        path = Path(source_path)
        if path.suffix != ".py" or not path.stem.isidentifier() or not function_name.isidentifier():
            raise ValueError("source must be a Python file and function_name a Python identifier")
        uploaded = self.upload_function(function_id, path)
        version = self.deploy_function(
            function_id,
            artifact_key=uploaded["artifact_key"],
            image=image,
            entrypoint=f"{path.stem}:{function_name}",
            **version_settings,
        )
        return self.function(function_id, version["id"])

    def spawn_function(
        self,
        function_id: str,
        version_id: str,
        args: tuple[Any, ...],
        kwargs: dict[str, Any],
    ) -> dict[str, Any]:
        response = requests.post(
            f"{self.base_url}/v1/functions/{quote(function_id, safe='')}/jobs",
            json={
                "id": str(uuid4()),
                "version_id": version_id,
                "input": {"args": list(args), "kwargs": kwargs},
            },
            timeout=10,
        )
        response.raise_for_status()
        return response.json()

    def get_job(self, job_id: str) -> dict[str, Any]:
        response = requests.get(
            f"{self.base_url}/v1/jobs/{quote(job_id, safe='')}",
            timeout=10,
        )
        response.raise_for_status()
        return response.json()

    def get_job_result(self, job_id: str) -> dict[str, Any]:
        response = requests.get(
            f"{self.base_url}/v1/jobs/{quote(job_id, safe='')}/result",
            timeout=10,
        )
        response.raise_for_status()
        return response.json()

    def cancel_job(self, job_id: str) -> dict[str, Any]:
        response = requests.post(
            f"{self.base_url}/v1/jobs/{quote(job_id, safe='')}/cancel",
            timeout=10,
        )
        response.raise_for_status()
        return response.json()

    def get_job_logs(self, job_id: str, attempt: int | None = None) -> list[dict[str, Any]]:
        response = requests.get(
            f"{self.base_url}/v1/jobs/{quote(job_id, safe='')}/logs",
            params={"attempt": attempt} if attempt is not None else None,
            timeout=10,
        )
        response.raise_for_status()
        return response.json()

    def get_job_events(self, job_id: str) -> list[dict[str, Any]]:
        response = requests.get(
            f"{self.base_url}/v1/jobs/{quote(job_id, safe='')}/events",
            timeout=10,
        )
        response.raise_for_status()
        return response.json()


class Function:
    def __init__(self, function_id: str, client: Client, version_id: str):
        self.id = function_id
        self.client = client
        self.version_id = version_id

    def spawn(self, *args: Any, **kwargs: Any) -> JobHandle:
        job = self.client.spawn_function(self.id, self.version_id, args, kwargs)
        return JobHandle(job["id"], self.client)

    def remote(self, *args: Any, **kwargs: Any) -> Any:
        return self.spawn(*args, **kwargs).get()


@dataclass(frozen=True)
class FunctionSettings:
    image: str = _DEFAULT_PYTHON_IMAGE
    cpus: int = 1
    memory_gb: int = 1
    gpu_type: str = ""
    gpus: int = 0
    timeout_seconds: int = 300
    max_attempts: int = 1
    retry_backoff_seconds: int = 0


class DecoratedFunction:
    def __init__(
        self,
        function_id: str,
        source: Callable[..., Any],
        settings: FunctionSettings,
        client: Client,
    ):
        update_wrapper(self, source)
        self.id = function_id
        self.source = source
        self.settings = settings
        self.client = client
        self.version_id: str | None = None

    def __call__(self, *args: Any, **kwargs: Any) -> Any:
        return self.source(*args, **kwargs)

    def bind_version(self, version_id: str) -> None:
        if not version_id:
            raise ValueError("version ID is required")
        self.version_id = version_id

    def spawn(self, *args: Any, **kwargs: Any) -> JobHandle:
        if self.version_id is None:
            raise RuntimeError(f"function {self.id!r} has no deployed version yet")
        return self.client.function(self.id, self.version_id).spawn(*args, **kwargs)

    def remote(self, *args: Any, **kwargs: Any) -> Any:
        return self.spawn(*args, **kwargs).get()


class App:
    def __init__(self, name: str, client: Client | None = None):
        if not name.strip():
            raise ValueError("app name is required")
        self.name = name
        self.client = client if client is not None else Client()
        self.functions: dict[str, DecoratedFunction] = {}

    def function(
        self,
        *,
        image: str = _DEFAULT_PYTHON_IMAGE,
        cpus: int = 1,
        memory_gb: int = 1,
        gpu_type: str = "",
        gpus: int = 0,
        timeout_seconds: int = 300,
        max_attempts: int = 1,
        retry_backoff_seconds: int = 0,
    ) -> Callable[[Callable[..., Any]], DecoratedFunction]:
        settings = FunctionSettings(
            image=image,
            cpus=cpus,
            memory_gb=memory_gb,
            gpu_type=gpu_type,
            gpus=gpus,
            timeout_seconds=timeout_seconds,
            max_attempts=max_attempts,
            retry_backoff_seconds=retry_backoff_seconds,
        )

        def register(source: Callable[..., Any]) -> DecoratedFunction:
            if source.__name__ in self.functions:
                raise ValueError(f"function {source.__name__!r} is already registered")
            decorated = DecoratedFunction(
                f"{self.name}.{source.__name__}", source, settings, self.client
            )
            self.functions[source.__name__] = decorated
            return decorated

        return register

    def deploy(self) -> dict[str, Function]:
        if not self.functions:
            raise ValueError("app has no registered functions")

        deployed: dict[str, Function] = {}
        for name, decorated in self.functions.items():
            source = decorated.source
            if source.__qualname__ != source.__name__:
                raise ValueError(f"function {name!r} must be defined at module level")
            source_path = inspect.getsourcefile(source)
            if source_path is None:
                raise ValueError(f"could not find source file for function {name!r}")
            path = Path(source_path)
            if path.suffix != ".py" or not path.stem.isidentifier():
                raise ValueError(f"function {name!r} must be defined in a Python module")

            print(f"{decorated.id}: uploading source...", file=sys.stderr, flush=True)
            uploaded = self.client.upload_function(decorated.id, path)
            settings = asdict(decorated.settings)
            settings["max_attempts"] = settings["max_attempts"] or _DEFAULT_MAX_ATTEMPTS
            settings["retry_backoff_seconds"] = settings["retry_backoff_seconds"] or _DEFAULT_RETRY_BACKOFF_SECONDS
            spec = {
                "artifact_key": uploaded["artifact_key"],
                "entrypoint": f"{path.stem}:{source.__name__}",
                **settings,
            }

            print(f"{decorated.id}: checking versions...", file=sys.stderr, flush=True)
            versions = self.client.get_function_versions(decorated.id)
            existing = next(
                (version for version in versions if all(version.get(key) == value for key, value in spec.items())),
                None,
            )
            if existing is not None:
                remote = self.client.function(decorated.id, existing["id"])
                print(f"{decorated.id}: reused version {remote.version_id}", file=sys.stderr, flush=True)
            else:
                print(f"{decorated.id}: creating version...", file=sys.stderr, flush=True)
                version = self.client.deploy_function(decorated.id, **spec)
                remote = self.client.function(decorated.id, version["id"])
                print(f"{decorated.id}: created version {remote.version_id}", file=sys.stderr, flush=True)
            decorated.bind_version(remote.version_id)
            deployed[name] = remote

        return deployed


class JobHandle:
    def __init__(self, job_id: str, client: Client):
        self.job_id = job_id
        self.client = client

    def status(self) -> dict[str, Any]:
        return self.client.get_job(self.job_id)

    def cancel(self) -> dict[str, Any]:
        return self.client.cancel_job(self.job_id)

    def logs(self, attempt: int | None = None) -> list[dict[str, Any]]:
        return self.client.get_job_logs(self.job_id, attempt)

    def events(self) -> list[dict[str, Any]]:
        return self.client.get_job_events(self.job_id)

    def get(self, timeout: float = 300.0, poll_interval: float = 1.0) -> Any:
        if timeout <= 0 or poll_interval <= 0:
            raise ValueError("timeout and poll_interval must be positive")

        deadline = time.monotonic() + timeout
        while True:
            if time.monotonic() >= deadline:
                raise TimeoutError(f"job {self.job_id!r} did not finish within {timeout} seconds")

            job = self.status()
            status = job["status"]
            if status == "succeeded":
                result = self.client.get_job_result(self.job_id)
                if "output_json" in result:
                    return result["output_json"]
            elif status == "failed":
                stderr = ""
                try:
                    logs = self.logs(job["attempt"] if job.get("attempt", 0) > 0 else None)
                    stderr = "".join(
                        chunk["data"] for chunk in logs if chunk.get("source") == "stderr"
                    )[-8192:]
                except requests.RequestException:
                    pass
                raise FunctionFailedError(self.job_id, job.get("last_error", ""), stderr)
            elif status == "cancelled":
                raise RuntimeError(f"job {self.job_id!r} was cancelled")
            elif status not in _WAITING_STATUSES:
                raise ValueError(f"unknown job status: {status!r}")

            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(f"job {self.job_id!r} did not finish within {timeout} seconds")
            time.sleep(min(poll_interval, remaining))
