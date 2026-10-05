import hashlib
import io
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
from contextlib import redirect_stderr
from pathlib import Path
from unittest.mock import Mock, patch

import requests

from mini_modal import App, Client, FunctionFailedError, JobHandle, combine_sources_into_tarball
from mini_modal._deployment_cache import cached_version, save_version
from mini_modal._packaging import collect_local_sources


def _sdk_test_greet(name):
    return f"Hello, {name}!"


def _generated_artifact_key(source: str) -> str:
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        archive = combine_sources_into_tarball(
            root, {"_sdk_test_greet.py": source.encode("utf-8")}
        )
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    return f"functions/demo/artifacts/{digest}.tar.gz"


class AppTests(unittest.TestCase):
    def setUp(self):
        self.client = Client("http://localhost:8080")
        cache_lookup = patch("mini_modal.cached_version", return_value=None)
        cache_save = patch("mini_modal.save_version")
        cache_lookup.start()
        cache_save.start()
        self.addCleanup(cache_lookup.stop)
        self.addCleanup(cache_save.stop)

    @patch("mini_modal.requests.get")
    @patch("mini_modal.requests.post")
    def test_decorator_only_registers_and_local_call_stays_local(self, post, get):
        app = App("demo", self.client)

        @app.function(image="mini-modal-python:dev", cpus=2, timeout_seconds=60)
        def greet(name: str) -> str:
            """Return a greeting."""
            return f"Hello, {name}!"

        self.assertIs(app.functions["greet"], greet)
        self.assertEqual(greet.id, "demo.greet")
        self.assertEqual(greet.__name__, "greet")
        self.assertEqual(greet.__doc__, "Return a greeting.")
        self.assertEqual(greet.settings.image, "mini-modal-python:dev")
        self.assertEqual(greet.settings.cpus, 2)
        self.assertEqual(greet.settings.timeout_seconds, 60)
        self.assertEqual(greet("Ada"), "Hello, Ada!")
        post.assert_not_called()
        get.assert_not_called()

    def test_spawn_requires_a_deployed_version(self):
        app = App("demo", self.client)

        @app.function(image="mini-modal-python:dev")
        def greet(name):
            return name

        with self.assertRaisesRegex(RuntimeError, "no deployed version"):
            greet.spawn("Ada")

        greet.bind_version("version-1")
        with patch.object(self.client, "spawn_function", return_value={"id": "job-1"}) as spawn:
            job = greet.spawn("Ada", excited=True)
        self.assertEqual(job.job_id, "job-1")
        spawn.assert_called_once_with("demo.greet", "version-1", ("Ada",), {"excited": True})

    def test_duplicate_function_name_is_rejected(self):
        app = App("demo", self.client)

        @app.function(image="mini-modal-python:dev")
        def greet():
            return "first"

        with self.assertRaisesRegex(ValueError, "already registered"):
            @app.function(image="mini-modal-python:dev")
            def greet():
                return "second"

        self.assertEqual(app.functions["greet"](), "first")

    @patch("mini_modal.requests.get")
    @patch("mini_modal.requests.post")
    def test_deploy_uploads_source_creates_version_and_binds_function(self, post, get):
        app = App("demo", self.client)
        greet = app.function(cpus=2)(_sdk_test_greet)
        get.return_value.json.return_value = []
        post.side_effect = [
            Mock(**{"json.return_value": {"artifact_key": "artifact-1"}}),
            Mock(**{"json.return_value": {"id": "version-1"}}),
        ]

        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "greeting.py"
            source.write_text("def _sdk_test_greet(name): return name\n")
            progress = io.StringIO()
            with patch("mini_modal.inspect.getsourcefile", return_value=str(source)):
                with redirect_stderr(progress):
                    deployed = app.deploy()

        self.assertEqual(deployed["_sdk_test_greet"].version_id, "version-1")
        self.assertEqual(greet.version_id, "version-1")
        upload_request = post.call_args_list[0].kwargs
        self.assertIsNone(upload_request["params"])
        self.assertEqual(upload_request["headers"], {"Content-Type": "application/gzip"})
        with tarfile.open(fileobj=io.BytesIO(upload_request["data"]), mode="r:gz") as archive:
            self.assertEqual(archive.getnames(), ["_sdk_test_greet.py"])
            self.assertEqual(
                archive.extractfile("_sdk_test_greet.py").read(),
                b"def _sdk_test_greet(name):\n    return name",
            )
        version_request = post.call_args_list[1].kwargs["json"]
        self.assertEqual(version_request["entrypoint"], "_sdk_test_greet:_sdk_test_greet")
        self.assertEqual(version_request["image"], "mini-modal-python:dev")
        self.assertEqual(version_request["cpus"], 2)
        self.assertEqual(version_request["retry_backoff_seconds"], 1)
        self.assertIn("created version version-1", progress.getvalue())
        get.assert_called_once()

    def test_deploy_reuses_matching_version_and_reports_it(self):
        app = App("demo", self.client)
        greet = app.function(cpus=2, max_attempts=0)(_sdk_test_greet)
        artifact_key = _generated_artifact_key(
            "def _sdk_test_greet(name):\n    return f'Hello, {name}!'"
        )
        existing = {
            "id": "version-1",
            "artifact_key": artifact_key,
            "entrypoint": "_sdk_test_greet:_sdk_test_greet",
            "image": "mini-modal-python:dev",
            "cpus": 2,
            "memory_gb": 1,
            "gpu_type": "",
            "gpus": 0,
            "timeout_seconds": 300,
            "max_attempts": 3,
            "retry_backoff_seconds": 1,
        }
        progress = io.StringIO()

        with patch("mini_modal.inspect.getsourcefile", return_value=__file__), \
                patch.object(self.client, "upload_function") as upload, \
                patch.object(self.client, "get_function_versions", return_value=[existing]), \
                patch.object(self.client, "deploy_function") as create, \
                redirect_stderr(progress):
            deployed = app.deploy()

        self.assertEqual(deployed["_sdk_test_greet"].version_id, "version-1")
        self.assertEqual(greet.version_id, "version-1")
        upload.assert_not_called()
        create.assert_not_called()
        self.assertIn("reused version version-1", progress.getvalue())

    def test_deploy_creates_version_when_source_or_settings_change(self):
        matching_key = _generated_artifact_key(
            "def _sdk_test_greet(name):\n    return f'Hello, {name}!'"
        )
        existing = {
            "id": "version-1",
            "artifact_key": matching_key,
            "entrypoint": "_sdk_test_greet:_sdk_test_greet",
            "image": "mini-modal-python:dev",
            "cpus": 1,
            "memory_gb": 1,
            "gpu_type": "",
            "gpus": 0,
            "timeout_seconds": 300,
            "max_attempts": 1,
            "retry_backoff_seconds": 1,
        }
        for remote_key, cpus, expected_key, should_upload in [
            ("old-artifact", 1, "artifact-2", True),
            (matching_key, 2, matching_key, False),
        ]:
            with self.subTest(remote_key=remote_key, cpus=cpus):
                app = App("demo", self.client)
                greet = app.function(cpus=cpus)(_sdk_test_greet)
                existing_version = {**existing, "artifact_key": remote_key}
                with patch("mini_modal.inspect.getsourcefile", return_value=__file__), \
                        patch.object(self.client, "upload_function", return_value={"artifact_key": expected_key}) as upload, \
                        patch.object(self.client, "get_function_versions", return_value=[existing_version]), \
                        patch.object(self.client, "deploy_function", return_value={"id": "version-2"}) as create, \
                        redirect_stderr(io.StringIO()):
                    app.deploy()

                self.assertEqual(greet.version_id, "version-2")
                self.assertEqual(upload.call_count, int(should_upload))
                self.assertEqual(create.call_args.kwargs["artifact_key"], expected_key)
                self.assertEqual(create.call_args.kwargs["cpus"], cpus)

    def test_deploy_rejects_nested_function_before_upload(self):
        app = App("demo", self.client)

        @app.function(image="mini-modal-python:dev")
        def nested():
            return "local"

        with patch.object(self.client, "deploy_python_file") as deploy:
            with self.assertRaisesRegex(ValueError, "module level"):
                app.deploy()
        deploy.assert_not_called()
        self.assertIsNone(nested.version_id)

    def test_deploy_removes_temporary_source_when_upload_fails(self):
        app = App("demo", self.client)
        app.function()(_sdk_test_greet)

        with patch.object(self.client, "upload_function", side_effect=RuntimeError("upload failed")) as upload:
            with self.assertRaisesRegex(RuntimeError, "upload failed"):
                app.deploy()

        temporary_source = upload.call_args.args[1]
        self.assertEqual(temporary_source.name, "combined.tar.gz")
        self.assertFalse(temporary_source.exists())

    def test_deploy_bundles_local_import_and_removes_temporary_archive(self):
        app = App("demo", self.client)
        app.function()(_sdk_test_greet)
        uploaded_paths = []

        def inspect_upload(function_id, path):
            self.assertEqual(function_id, "demo._sdk_test_greet")
            self.assertTrue(path.is_file())
            uploaded_paths.append(path)
            with tarfile.open(path, "r:gz") as archive:
                self.assertEqual(
                    archive.getnames(), ["_sdk_test_greet.py", "helpers.py"]
                )
                self.assertIn(
                    b"from helpers import helper",
                    archive.extractfile("_sdk_test_greet.py").read(),
                )
            return {"artifact_key": "artifact-1"}

        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "main.py"
            source.write_text(
                "from helpers import helper\n"
                "def _sdk_test_greet(name):\n"
                "    return helper(name)\n"
            )
            (Path(directory) / "helpers.py").write_text(
                "def helper(name):\n    return name\n"
            )
            with patch("mini_modal.inspect.getsourcefile", return_value=str(source)), \
                    patch.object(self.client, "upload_function", side_effect=inspect_upload), \
                    patch.object(self.client, "get_function_versions", return_value=[]), \
                    patch.object(self.client, "deploy_function", return_value={"id": "version-1"}):
                app.deploy()

        self.assertEqual(len(uploaded_paths), 1)
        self.assertFalse(uploaded_paths[0].exists())

    def test_deploy_preserves_package_paths_and_entrypoint(self):
        app = App("demo", self.client)
        app.function()(_sdk_test_greet)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            package = root / "example"
            package.mkdir()
            (package / "__init__.py").write_text("", encoding="utf-8")
            source = package / "app.py"
            source.write_text(
                "from .helpers import greet\n"
                "def _sdk_test_greet(name):\n"
                "    return greet(name)\n",
                encoding="utf-8",
            )
            (package / "helpers.py").write_text(
                "from .formatting import suffix\n"
                "def greet(name):\n"
                "    return name + suffix()\n",
                encoding="utf-8",
            )
            (package / "formatting.py").write_text(
                "def suffix():\n    return '!'\n", encoding="utf-8"
            )

            def check_upload(function_id, archive_path):
                with tarfile.open(archive_path, "r:gz") as archive:
                    self.assertEqual(archive.getnames(), [
                        "example/__init__.py",
                        "example/_sdk_test_greet.py",
                        "example/formatting.py",
                        "example/helpers.py",
                    ])
                    self.assertIn(
                        b"from .helpers import greet",
                        archive.extractfile("example/_sdk_test_greet.py").read(),
                    )
                    runtime = root / "runtime"
                    runtime.mkdir()
                    archive.extractall(runtime)
                result = subprocess.run(
                    [sys.executable, "-c", "from example._sdk_test_greet import _sdk_test_greet; print(_sdk_test_greet('Ada'))"],
                    cwd=runtime,
                    check=True,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.stdout.strip(), "Ada!")
                return {"artifact_key": "artifact-1"}

            with patch("mini_modal.inspect.getsourcefile", return_value=str(source)), \
                    patch.object(self.client, "get_function_versions", return_value=[]), \
                    patch.object(self.client, "upload_function", side_effect=check_upload), \
                    patch.object(self.client, "deploy_function", return_value={"id": "version-1"}) as create:
                app.deploy()

            self.assertEqual(
                create.call_args.kwargs["entrypoint"],
                "example._sdk_test_greet:_sdk_test_greet",
            )

    def test_decorator_includes_dynamic_import_package(self):
        app = App("demo", self.client)
        app.function(include_modules=("plugins",))(_sdk_test_greet)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "app.py"
            source.write_text(
                "import importlib\n"
                "def _sdk_test_greet(name):\n"
                "    return importlib.import_module('plugins.' + name).VALUE\n",
                encoding="utf-8",
            )
            plugins = root / "plugins"
            plugins.mkdir()
            (plugins / "__init__.py").write_text("", encoding="utf-8")
            (plugins / "first.py").write_text("VALUE = 1\n", encoding="utf-8")

            def check_upload(function_id, archive_path):
                with tarfile.open(archive_path, "r:gz") as archive:
                    self.assertEqual(archive.getnames(), [
                        "_sdk_test_greet.py", "plugins/__init__.py", "plugins/first.py"
                    ])
                    runtime = root / "runtime"
                    runtime.mkdir()
                    archive.extractall(runtime)
                result = subprocess.run(
                    [sys.executable, "-c", "from _sdk_test_greet import _sdk_test_greet; print(_sdk_test_greet('first'))"],
                    cwd=runtime,
                    check=True,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.stdout.strip(), "1")
                return {"artifact_key": "artifact-1"}

            with patch("mini_modal.inspect.getsourcefile", return_value=str(source)), \
                    patch.object(self.client, "get_function_versions", return_value=[]), \
                    patch.object(self.client, "upload_function", side_effect=check_upload), \
                    patch.object(self.client, "deploy_function", return_value={"id": "version-1"}):
                app.deploy()

    def test_archive_bytes_ignore_file_mtimes_and_change_with_helper_content(self):
        archives = []
        with tempfile.TemporaryDirectory() as first, tempfile.TemporaryDirectory() as second:
            for index, directory in enumerate((first, second)):
                root = Path(directory)
                entry = root / "entry.py"
                helper = root / "helpers.py"
                entry.write_text("from helpers import value\n", encoding="utf-8")
                helper.write_text("value = 1\n", encoding="utf-8")
                os.utime(entry, (100 + index, 100 + index))
                os.utime(helper, (200 + index, 200 + index))
                archives.append(combine_sources_into_tarball(root, {
                    "entry.py": entry.read_bytes(), "helpers.py": helper.read_bytes()
                }).read_bytes())

            self.assertEqual(archives[0], archives[1])
            helper.write_text("value = 2\n", encoding="utf-8")
            changed = combine_sources_into_tarball(Path(second), {
                "entry.py": entry.read_bytes(), "helpers.py": helper.read_bytes()
            }).read_bytes()
            self.assertNotEqual(archives[0], changed)

    def test_deploy_skips_unchanged_content_and_uploads_changed_helper(self):
        app = App("demo", self.client)
        app.function()(_sdk_test_greet)
        remote_versions = []
        uploaded_archives = []

        def upload(function_id, path):
            content = path.read_bytes()
            uploaded_archives.append(content)
            digest = hashlib.sha256(content).hexdigest()
            return {"artifact_key": f"functions/demo/artifacts/{digest}.tar.gz"}

        def create(function_id, **spec):
            version = {"id": f"version-{len(remote_versions) + 1}", **spec}
            remote_versions.append(version)
            return version

        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "main.py"
            source.write_text(
                "from helpers import helper\n"
                "def _sdk_test_greet(name):\n"
                "    return helper(name)\n"
            )
            helper = Path(directory) / "helpers.py"
            helper.write_text("def helper(name):\n    return name\n")

            with patch("mini_modal.inspect.getsourcefile", return_value=str(source)), \
                    patch.object(self.client, "upload_function", side_effect=upload), \
                    patch.object(self.client, "get_function_versions", side_effect=lambda function_id: list(remote_versions)), \
                    patch.object(self.client, "deploy_function", side_effect=create):
                first = app.deploy()["_sdk_test_greet"]
                unchanged = app.deploy()["_sdk_test_greet"]
                helper.write_text("def helper(name):\n    return name.upper()\n")
                changed = app.deploy()["_sdk_test_greet"]

        self.assertEqual(first.version_id, unchanged.version_id)
        self.assertNotEqual(first.version_id, changed.version_id)
        self.assertEqual(len(uploaded_archives), 2)
        self.assertEqual(len(remote_versions), 2)


class DeploymentCacheTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.cache_dir = self.root / ".mini-modal"
        self.client = Client("http://localhost:8080")

    def app(self, *, cpus=1):
        app = App("demo", self.client, cache_dir=self.cache_dir)
        decorated = app.function(cpus=cpus)(_sdk_test_greet)
        return app, decorated

    def test_cache_is_scoped_and_corrupt_entries_are_ignored(self):
        save_version(self.cache_dir, self.client.base_url, "demo.greet", "fingerprint-1", "version-1")
        self.assertEqual(
            cached_version(self.cache_dir, self.client.base_url, "demo.greet", "fingerprint-1"),
            "version-1",
        )
        self.assertIsNone(cached_version(self.cache_dir, "http://other:8080", "demo.greet", "fingerprint-1"))
        self.assertIsNone(cached_version(self.cache_dir, self.client.base_url, "demo.other", "fingerprint-1"))
        self.assertIsNone(cached_version(self.cache_dir, self.client.base_url, "demo.greet", "fingerprint-2"))
        next((self.cache_dir / "deployments").glob("*.json")).write_text("{broken", encoding="utf-8")
        self.assertIsNone(cached_version(self.cache_dir, self.client.base_url, "demo.greet", "fingerprint-1"))

    def test_unchanged_restart_uses_persisted_version_without_network(self):
        first_app, _ = self.app()
        with patch("mini_modal.inspect.getsourcefile", return_value=__file__), \
                patch.object(self.client, "get_function_versions", return_value=[]) as versions, \
                patch.object(self.client, "upload_function", return_value={"artifact_key": "artifact-1"}) as upload, \
                patch.object(self.client, "deploy_function", return_value={"id": "version-1"}) as create, \
                redirect_stderr(io.StringIO()):
            first_app.deploy()
            restarted_app, decorated = self.app()
            restarted_app.deploy()

        self.assertEqual(decorated.version_id, "version-1")
        versions.assert_called_once()
        upload.assert_called_once()
        create.assert_called_once()
        self.assertEqual(len(list((self.cache_dir / "deployments").glob("*.json"))), 1)

    def test_helper_or_settings_change_invalidates_only_matching_fingerprint(self):
        source = self.root / "main.py"
        helper = self.root / "helper.py"
        source.write_text(
            "from helper import suffix\n"
            "def _sdk_test_greet(name):\n"
            "    return name + suffix()\n",
            encoding="utf-8",
        )
        helper.write_text("def suffix(): return '!'\n", encoding="utf-8")
        remote_versions = []

        def upload(function_id, path):
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            return {"artifact_key": f"functions/demo/artifacts/{digest}.tar.gz"}

        def create(function_id, **spec):
            version = {"id": f"version-{len(remote_versions) + 1}", **spec}
            remote_versions.append(version)
            return version

        with patch("mini_modal.inspect.getsourcefile", return_value=str(source)), \
                patch.object(self.client, "get_function_versions", side_effect=lambda _: list(remote_versions)) as versions, \
                patch.object(self.client, "upload_function", side_effect=upload) as uploads, \
                patch.object(self.client, "deploy_function", side_effect=create) as creates, \
                redirect_stderr(io.StringIO()):
            first, _ = self.app()
            first.deploy()
            unchanged, _ = self.app()
            unchanged.deploy()
            helper.write_text("def suffix(): return '?'\n", encoding="utf-8")
            changed_source, _ = self.app()
            changed_source.deploy()
            changed_settings, decorated = self.app(cpus=2)
            changed_settings.deploy()
            unchanged_settings, _ = self.app(cpus=2)
            unchanged_settings.deploy()

        self.assertEqual(decorated.version_id, "version-3")
        self.assertEqual(versions.call_count, 3)
        self.assertEqual(uploads.call_count, 2)
        self.assertEqual(creates.call_count, 3)

    def test_missing_cached_version_refreshes_and_retries_invocation(self):
        remote_versions = []
        created = 0

        def create(function_id, **spec):
            nonlocal created
            created += 1
            version = {"id": f"version-{created}", **spec}
            remote_versions.append(version)
            return version

        with patch("mini_modal.inspect.getsourcefile", return_value=__file__), \
                patch.object(self.client, "get_function_versions", side_effect=lambda _: list(remote_versions)) as versions, \
                patch.object(self.client, "upload_function", return_value={"artifact_key": "artifact-1"}) as upload, \
                patch.object(self.client, "deploy_function", side_effect=create), \
                redirect_stderr(io.StringIO()):
            first, _ = self.app()
            first.deploy()
            restarted, decorated = self.app()
            restarted.deploy()
            remote_versions.clear()  # Simulate a reset control plane after the local cache was read.
            response = requests.Response()
            response.status_code = 404
            missing = requests.HTTPError(response=response)
            with patch.object(
                self.client, "spawn_function", side_effect=[missing, {"id": "job-1"}]
            ) as spawn:
                job = decorated.spawn("Ada")

        self.assertEqual(job.job_id, "job-1")
        self.assertEqual(decorated.version_id, "version-2")
        self.assertEqual(versions.call_count, 2)
        self.assertEqual(upload.call_count, 2)
        self.assertEqual([call.args[1] for call in spawn.call_args_list], ["version-1", "version-2"])


class PackagingTests(unittest.TestCase):
    def test_transitive_and_function_local_imports_are_included(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "helpers.py").write_text(
                "from formatting import suffix\n"
                "def greet(name):\n    return name + suffix()\n",
                encoding="utf-8",
            )
            (root / "formatting.py").write_text("def suffix():\n    return '!'\n", encoding="utf-8")
            source = "def greet(name):\n    from helpers import greet as helper\n    return helper(name)\n"
            files = collect_local_sources(root, Path("greet.py"), source, ())
            self.assertEqual(set(files), {"greet.py", "helpers.py", "formatting.py"})
            first_archive = combine_sources_into_tarball(root, files).read_bytes()
            (root / "formatting.py").write_text("def suffix():\n    return '?'\n", encoding="utf-8")
            changed_files = collect_local_sources(root, Path("greet.py"), source, ())
            changed_archive = combine_sources_into_tarball(root, changed_files).read_bytes()
            self.assertNotEqual(first_archive, changed_archive)

    def test_explicit_include_bundles_dynamic_import_package(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plugins = root / "plugins"
            plugins.mkdir()
            (plugins / "__init__.py").write_text("", encoding="utf-8")
            (plugins / "first.py").write_text("VALUE = 1\n", encoding="utf-8")
            (plugins / "second.py").write_text("VALUE = 2\n", encoding="utf-8")
            source = (
                "import importlib\n"
                "def run(name):\n"
                "    return importlib.import_module('plugins.' + name).VALUE\n"
            )
            omitted = collect_local_sources(root, Path("run.py"), source, ())
            self.assertEqual(set(omitted), {"run.py"})
            included = collect_local_sources(root, Path("run.py"), source, ("plugins",))
            self.assertEqual(set(included), {
                "run.py", "plugins/__init__.py", "plugins/first.py", "plugins/second.py"
            })

    def test_nested_packages_keep_distinct_modules_with_same_basename(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "pkg").mkdir()
            (root / "pkg" / "__init__.py").write_text("", encoding="utf-8")
            for name in ("alpha", "beta"):
                package = root / "pkg" / name
                package.mkdir()
                (package / "__init__.py").write_text("", encoding="utf-8")
                (package / "helpers.py").write_text(
                    f"VALUE = {name!r}\n", encoding="utf-8"
                )
            files = collect_local_sources(
                root,
                Path("run.py"),
                "import pkg.alpha.helpers\nimport pkg.beta.helpers\n",
                (),
            )
            self.assertEqual(set(files), {
                "run.py", "pkg/__init__.py",
                "pkg/alpha/__init__.py", "pkg/alpha/helpers.py",
                "pkg/beta/__init__.py", "pkg/beta/helpers.py",
            })
            self.assertNotEqual(
                files["pkg/alpha/helpers.py"], files["pkg/beta/helpers.py"]
            )

    def test_missing_explicit_include_is_an_error(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(ValueError, "was not found"):
                collect_local_sources(
                    Path(directory), Path("run.py"), "def run(): pass", ("missing",)
                )


class ClientTests(unittest.TestCase):
    def setUp(self):
        self.client = Client("http://localhost:8080/")

    @patch("mini_modal.requests.post")
    def test_deploy_python_file_uploads_and_creates_version(self, post):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "hello_function.py"
            source.write_text("def greet(name): return name\n")
            responses = [
                Mock(**{"json.return_value": {"artifact_key": "artifact-1"}}),
                Mock(**{"json.return_value": {"id": "version-1"}}),
            ]
            post.side_effect = responses

            function = self.client.deploy_python_file(
                "greet", source, "greet", cpus=2
            )

        self.assertEqual((function.id, function.version_id), ("greet", "version-1"))
        upload = post.call_args_list[0]
        self.assertEqual(upload.args[0], "http://localhost:8080/v1/functions/greet/artifacts")
        self.assertEqual(upload.kwargs["params"], {"filename": "hello_function.py"})
        self.assertEqual(upload.kwargs["headers"], {"Content-Type": "text/x-python"})
        self.assertEqual(upload.kwargs["data"], b"def greet(name): return name\n")
        deploy = post.call_args_list[1]
        self.assertEqual(deploy.args[0], "http://localhost:8080/v1/functions/greet/versions")
        self.assertEqual(deploy.kwargs["json"]["artifact_key"], "artifact-1")
        self.assertEqual(deploy.kwargs["json"]["entrypoint"], "hello_function:greet")
        self.assertEqual(deploy.kwargs["json"]["image"], "mini-modal-python:dev")
        self.assertEqual(deploy.kwargs["json"]["cpus"], 2)
        for response in responses:
            response.raise_for_status.assert_called_once()

    @patch("mini_modal.requests.post")
    def test_spawn_sends_args_kwargs_and_returns_handle(self, post):
        post.return_value.json.return_value = {"id": "job-1", "status": "pending"}
        call = self.client.function("greet", "version-1").spawn("Ada", excited=True)

        self.assertIsInstance(call, JobHandle)
        self.assertEqual(call.job_id, "job-1")
        self.assertEqual(post.call_args.args[0], "http://localhost:8080/v1/functions/greet/jobs")
        payload = post.call_args.kwargs["json"]
        self.assertEqual(payload["version_id"], "version-1")
        self.assertEqual(payload["input"], {"args": ["Ada"], "kwargs": {"excited": True}})
        self.assertTrue(payload["id"])

    @patch("mini_modal.requests.get")
    def test_get_waits_then_returns_output(self, get):
        get.side_effect = [
            Mock(**{"json.return_value": {"status": "pending"}}),
            Mock(**{"json.return_value": {"status": "running"}}),
            Mock(**{"json.return_value": {"status": "succeeded"}}),
            Mock(**{"json.return_value": {"output_json": {"message": "hello"}}}),
        ]
        with patch("mini_modal.time.sleep") as sleep:
            result = JobHandle("job-1", self.client).get()

        self.assertEqual(result, {"message": "hello"})
        self.assertEqual(sleep.call_count, 2)
        sleep.assert_any_call(0.25)
        self.assertEqual(get.call_args.args[0], "http://localhost:8080/v1/jobs/job-1/result")

    @patch("mini_modal.requests.get")
    def test_get_reports_python_exception_from_stderr(self, get):
        get.side_effect = [
            Mock(**{"json.return_value": {"status": "failed", "attempt": 2, "last_error": "exit status 1"}}),
            Mock(**{"json.return_value": [
                {"source": "stdout", "data": "starting\n"},
                {"source": "stderr", "data": "Traceback (most recent call last):\nRuntimeError: boom\n"},
            ]}),
        ]
        with self.assertRaises(FunctionFailedError) as raised:
            JobHandle("job-1", self.client).get()

        self.assertIn("RuntimeError: boom", str(raised.exception))
        self.assertEqual(raised.exception.last_error, "exit status 1")
        self.assertIn("Traceback", raised.exception.stderr)
        self.assertEqual(get.call_args.kwargs["params"], {"attempt": 2})

    def test_get_falls_back_when_logs_are_unavailable(self):
        with patch.object(self.client, "get_job", return_value={
            "status": "failed", "attempt": 1, "last_error": "exit status 1"
        }), patch.object(self.client, "get_job_logs", side_effect=requests.RequestException("offline")):
            with self.assertRaisesRegex(FunctionFailedError, "exit status 1"):
                JobHandle("job-1", self.client).get()

    @patch("mini_modal.requests.get")
    def test_get_reports_cancellation(self, get):
        get.return_value.json.return_value = {"status": "cancelled"}
        with self.assertRaisesRegex(RuntimeError, "cancelled"):
            JobHandle("job-1", self.client).get()

    @patch("mini_modal.requests.get")
    def test_get_times_out_while_pending(self, get):
        get.return_value.json.return_value = {"status": "pending"}
        with patch("mini_modal.time.sleep"):
            with self.assertRaises(TimeoutError):
                JobHandle("job-1", self.client).get(timeout=0.001)

    @patch("mini_modal.requests.post")
    def test_remote_waits_for_result(self, post):
        post.return_value.json.return_value = {"id": "job-1"}
        with patch.object(JobHandle, "get", return_value="Hello, Ada!") as get:
            result = self.client.function("greet", "version-1").remote("Ada")
        self.assertEqual(result, "Hello, Ada!")
        get.assert_called_once_with()

    @patch("mini_modal.requests.post")
    def test_cancel_uses_job_endpoint(self, post):
        post.return_value.json.return_value = {"id": "job-1", "status": "cancelled"}
        result = JobHandle("job-1", self.client).cancel()
        self.assertEqual(result["status"], "cancelled")
        self.assertEqual(post.call_args.args[0], "http://localhost:8080/v1/jobs/job-1/cancel")


if __name__ == "__main__":
    unittest.main()
