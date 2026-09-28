import io
import tempfile
import unittest
from contextlib import redirect_stderr
from pathlib import Path
from unittest.mock import Mock, patch

import requests

from mini_modal import App, Client, FunctionFailedError, JobHandle


def _sdk_test_greet(name):
    return f"Hello, {name}!"


class AppTests(unittest.TestCase):
    def setUp(self):
        self.client = Client("http://localhost:8080")

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
        self.assertEqual(post.call_args_list[0].kwargs["data"], b"def _sdk_test_greet(name): return name\n")
        version_request = post.call_args_list[1].kwargs["json"]
        self.assertEqual(version_request["entrypoint"], "greeting:_sdk_test_greet")
        self.assertEqual(version_request["image"], "mini-modal-python:dev")
        self.assertEqual(version_request["cpus"], 2)
        self.assertEqual(version_request["retry_backoff_seconds"], 1)
        self.assertIn("created version version-1", progress.getvalue())
        get.assert_called_once()

    def test_deploy_reuses_matching_version_and_reports_it(self):
        app = App("demo", self.client)
        greet = app.function(cpus=2, max_attempts=0)(_sdk_test_greet)
        existing = {
            "id": "version-1",
            "artifact_key": "artifact-1",
            "entrypoint": "test_client:_sdk_test_greet",
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
                patch.object(self.client, "upload_function", return_value={"artifact_key": "artifact-1"}), \
                patch.object(self.client, "get_function_versions", return_value=[existing]), \
                patch.object(self.client, "deploy_function") as create, \
                redirect_stderr(progress):
            deployed = app.deploy()

        self.assertEqual(deployed["_sdk_test_greet"].version_id, "version-1")
        self.assertEqual(greet.version_id, "version-1")
        create.assert_not_called()
        self.assertIn("reused version version-1", progress.getvalue())

    def test_deploy_creates_version_when_source_or_settings_change(self):
        existing = {
            "id": "version-1",
            "artifact_key": "artifact-1",
            "entrypoint": "test_client:_sdk_test_greet",
            "image": "mini-modal-python:dev",
            "cpus": 1,
            "memory_gb": 1,
            "gpu_type": "",
            "gpus": 0,
            "timeout_seconds": 300,
            "max_attempts": 1,
            "retry_backoff_seconds": 1,
        }
        for artifact_key, cpus in [("artifact-2", 1), ("artifact-1", 2)]:
            with self.subTest(artifact_key=artifact_key, cpus=cpus):
                app = App("demo", self.client)
                greet = app.function(cpus=cpus)(_sdk_test_greet)
                with patch("mini_modal.inspect.getsourcefile", return_value=__file__), \
                        patch.object(self.client, "upload_function", return_value={"artifact_key": artifact_key}), \
                        patch.object(self.client, "get_function_versions", return_value=[existing]), \
                        patch.object(self.client, "deploy_function", return_value={"id": "version-2"}) as create, \
                        redirect_stderr(io.StringIO()):
                    app.deploy()

                self.assertEqual(greet.version_id, "version-2")
                self.assertEqual(create.call_args.kwargs["artifact_key"], artifact_key)
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
        with patch("mini_modal.time.sleep"):
            result = JobHandle("job-1", self.client).get(poll_interval=0.01)

        self.assertEqual(result, {"message": "hello"})
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
