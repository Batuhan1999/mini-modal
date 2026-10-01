"""Collect local Python modules for a function artifact."""

from __future__ import annotations

import ast
import gzip
import io
import tarfile
from pathlib import Path


def source_package(source_file: Path) -> tuple[Path, tuple[str, ...]]:
    package_parts: list[str] = []
    directory = source_file.parent
    while (directory / "__init__.py").is_file():
        package_parts.insert(0, directory.name)
        directory = directory.parent
    return directory, tuple(package_parts)


def collect_local_sources(
    project_root: Path,
    entry_path: Path,
    reduced_source: str,
    include_modules: tuple[str, ...],
) -> dict[str, bytes]:
    project_root = project_root.resolve()
    sources = {entry_path.as_posix(): reduced_source.encode("utf-8")}
    pending = [(entry_path, sources[entry_path.as_posix()])]

    def add_file(path: Path) -> None:
        resolved = path.resolve()
        if not resolved.is_relative_to(project_root):
            raise ValueError(f"local module escapes project root: {path}")
        archive_path = path.relative_to(project_root).as_posix()
        content = path.read_bytes()
        if archive_path in sources:
            if sources[archive_path] != content:
                raise ValueError(f"generated function conflicts with local module {archive_path!r}")
            return
        sources[archive_path] = content
        pending.append((Path(archive_path), content))

        parent = path.parent
        while parent != project_root:
            init = parent / "__init__.py"
            if init.is_file():
                add_file(init)
            parent = parent.parent

    def add_module(parts: tuple[str, ...], *, recursive: bool = False) -> bool:
        if not parts:
            return False
        module_path = project_root.joinpath(*parts)
        module_file = module_path.with_suffix(".py")
        package_init = module_path / "__init__.py"
        if module_file.is_file():
            add_file(module_file)
            return True
        if package_init.is_file() or (recursive and module_path.is_dir()):
            if package_init.is_file():
                add_file(package_init)
            if recursive:
                for child in sorted(module_path.rglob("*.py")):
                    add_file(child)
            return True
        return False

    for module_name in include_modules:
        if not isinstance(module_name, str) or not module_name or not all(
            part.isidentifier() for part in module_name.split(".")
        ):
            raise ValueError(f"invalid included module name {module_name!r}")
        if not add_module(tuple(module_name.split(".")), recursive=True):
            raise ValueError(f"included local module {module_name!r} was not found")

    while pending:
        archive_path, text = pending.pop()
        package = archive_path.parent.parts if archive_path.parent != Path(".") else ()
        tree = ast.parse(text, filename=archive_path.as_posix())
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    add_module(tuple(alias.name.split(".")))
            elif isinstance(node, ast.ImportFrom):
                if node.level:
                    if node.level > len(package):
                        raise ValueError(f"relative import beyond package in {archive_path}")
                    base = package[: len(package) - node.level + 1]
                else:
                    base = ()
                module = base + tuple(node.module.split(".")) if node.module else base
                add_module(module)
                for alias in node.names:
                    if alias.name != "*":
                        add_module(module + (alias.name,))

    return sources


def combine_sources_into_tarball(directory: Path, sources: dict[str, bytes]) -> Path:
    archive = directory / "combined.tar.gz"
    with archive.open("wb") as output:
        with gzip.GzipFile(fileobj=output, mode="wb", filename="", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as tar:
                for name, content in sorted(sources.items()):
                    header = tarfile.TarInfo(name=name)
                    header.size = len(content)
                    header.mode = 0o644
                    header.mtime = 0
                    tar.addfile(header, io.BytesIO(content))
    return archive
