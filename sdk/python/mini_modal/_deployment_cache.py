"""Persistent local hints for previously deployed function versions."""

from __future__ import annotations

import hashlib
import json
import os
import tempfile
from pathlib import Path

_SCHEMA = 1


def fingerprint(content_hash: str, entrypoint: str, settings: dict[str, object]) -> str:
    payload = json.dumps(
        {"content_hash": content_hash, "entrypoint": entrypoint, "settings": settings},
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def _entry_path(cache_dir: Path, base_url: str, function_id: str) -> Path:
    key = hashlib.sha256(f"{base_url}\0{function_id}".encode("utf-8")).hexdigest()
    return cache_dir / "deployments" / f"{key}.json"


def cached_version(
    cache_dir: Path, base_url: str, function_id: str, current_fingerprint: str
) -> str | None:
    try:
        data = json.loads(_entry_path(cache_dir, base_url, function_id).read_text(encoding="utf-8"))
    except (OSError, ValueError, TypeError):
        return None
    if not isinstance(data, dict):
        return None
    if (
        data.get("schema") != _SCHEMA
        or data.get("base_url") != base_url
        or data.get("function_id") != function_id
        or data.get("fingerprint") != current_fingerprint
    ):
        return None
    version_id = data.get("version_id")
    return version_id if isinstance(version_id, str) and version_id else None


def save_version(
    cache_dir: Path, base_url: str, function_id: str, current_fingerprint: str, version_id: str
) -> None:
    path = _entry_path(cache_dir, base_url, function_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "schema": _SCHEMA,
        "base_url": base_url,
        "function_id": function_id,
        "fingerprint": current_fingerprint,
        "version_id": version_id,
    }
    temporary_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w", encoding="utf-8", dir=path.parent, prefix=f".{path.stem}-", delete=False
        ) as output:
            temporary_path = Path(output.name)
            json.dump(payload, output, sort_keys=True)
            output.write("\n")
        os.replace(temporary_path, path)
    finally:
        if temporary_path is not None:
            temporary_path.unlink(missing_ok=True)
