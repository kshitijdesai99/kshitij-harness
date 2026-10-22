"""Exercise the embedded server without torch, model downloads, or fixed ports."""
import http.client
import importlib.util
import json
from pathlib import Path
import threading
import time
import unittest
from unittest.mock import Mock, patch
import types

spec = importlib.util.spec_from_file_location("reranker", Path(__file__).parent.parent / "reranker.py")
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


class ServerTests(unittest.TestCase):
    def setUp(self):
        runtime.Handler.scorer = staticmethod(lambda q, docs: [
            {"index": i, "relevance_score": 0.5} for i in range(len(docs))])
        self.server = runtime.ThreadingHTTPServer(("127.0.0.1", 0), runtime.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def request(self, path="/rerank", data=None, method="POST"):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=2)
        try:
            conn.request(method, path, json.dumps(data))
            response = conn.getresponse()
            return response.status, json.loads(response.read())
        finally:
            conn.close()

    def test_health_identity_and_response_shape(self):
        status, data = self.request("/health", method="GET")
        self.assertEqual((status, data), (200, {"model": runtime.MODEL, "ready": True}))
        status, data = self.request(data={"query": "q", "documents": ["a", "b"]})
        self.assertEqual(status, 200)
        self.assertEqual([r["index"] for r in data["results"]], [0, 1])
        self.assertEqual(data["results"][0]["relevance_score"], 0.5)

    def test_invalid_requests(self):
        for data in [None, [], {}, {"query": "", "documents": ["d"]},
                     {"query": "q", "documents": []},
                     {"query": "q", "documents": [None]},
                     {"query": "q", "documents": ["d"] * 129}]:
            with self.subTest(data=data):
                self.assertEqual(self.request(data=data)[0], 400)
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=2)
        try:
            conn.putrequest("POST", "/rerank")
            conn.putheader("Content-Length", str(runtime.MAX_BODY + 1))
            conn.endheaders()
            response = conn.getresponse()
            self.assertEqual(response.status, 413)
            response.read()
        finally:
            conn.close()
        self.assertEqual(self.request("/other")[0], 404)

    def test_inference_failure_is_sanitized(self):
        def fail(*_):
            raise ValueError("private memory contents")
        runtime.Handler.scorer = staticmethod(fail)
        status, body = self.request(data={"query": "q", "documents": ["d"]})
        self.assertEqual(status, 500)
        self.assertNotIn("private", str(body))
        self.assertFalse(runtime.Handler.inference_lock.locked())

    def test_concurrent_callers_wait_and_health_remains_responsive(self):
        entered, release, waiting = threading.Event(), threading.Event(), threading.Event()
        class ObservedLock:
            def __init__(self): self.lock = threading.Lock()
            def acquire(self, *args, **kwargs):
                if self.lock.locked():
                    waiting.set()
                return self.lock.acquire(*args, **kwargs)
            def release(self): self.lock.release()
        def score(q, docs):
            entered.set()
            if not release.wait(2):
                raise TimeoutError("test did not release inference")
            return [{"index": 0, "relevance_score": 0.5}]
        runtime.Handler.scorer = staticmethod(score)
        responses = []
        def request():
            responses.append(self.request(data={"query": "q", "documents": ["d"]})[0])
        with patch.object(runtime.Handler, "inference_lock", ObservedLock()):
            first, second = threading.Thread(target=request), threading.Thread(target=request)
            first.start()
            try:
                self.assertTrue(entered.wait(1), "first inference did not start")
                second.start()
                self.assertTrue(waiting.wait(1), "second caller did not reach occupied lock")
                self.assertEqual(self.request("/health", method="GET")[0], 200)
            finally:
                release.set()
                first.join(2)
                if second.ident is not None:
                    second.join(2)
            self.assertFalse(first.is_alive())
            self.assertFalse(second.is_alive())
        self.assertEqual(sorted(responses), [200, 200])

    def test_three_worker_burst_completes_within_client_budget(self):
        # Scale wall time, preserving the production queue/inference ratio.
        # Each worker uses less than half the inference budget; two can still
        # occupy the lock long enough to reject the third with the old limits.
        scale = 0.025
        duration = runtime.INFERENCE_SECONDS * scale * 0.55
        barrier = threading.Barrier(3)
        responses = []
        def score(q, docs):
            time.sleep(duration)
            return [{"index": 0, "relevance_score": 0.5}]
        def request():
            barrier.wait()
            responses.append(self.request(data={"query": "q", "documents": ["d"]})[0])
        runtime.Handler.scorer = staticmethod(score)
        with patch.object(runtime, "LOCK_WAIT_SECONDS", runtime.LOCK_WAIT_SECONDS * scale):
            workers = [threading.Thread(target=request) for _ in range(3)]
            for worker in workers:
                worker.start()
            for worker in workers:
                worker.join(2)
                self.assertFalse(worker.is_alive())
        self.assertEqual(sorted(responses), [200, 200, 200])

    def test_queue_exhaustion_is_bounded_and_does_not_block_health(self):
        with patch.object(runtime, "LOCK_WAIT_SECONDS", 0.05, create=True):
            runtime.Handler.inference_lock.acquire()
            try:
                started = time.monotonic()
                self.assertEqual(self.request(data={"query": "q", "documents": ["d"]})[0], 503)
                elapsed = time.monotonic() - started
                self.assertGreaterEqual(elapsed, 0.04)
                self.assertLess(elapsed, 1)
                self.assertEqual(self.request("/health", method="GET")[0], 200)
            finally:
                runtime.Handler.inference_lock.release()

    def test_disconnected_client_does_not_raise_or_log(self):
        for error in (BrokenPipeError, ConnectionResetError):
            for stage in ("end_headers", "write"):
                with self.subTest(error=error, stage=stage):
                    handler = object.__new__(runtime.Handler)
                    handler.send_response = Mock()
                    handler.send_header = Mock()
                    handler.end_headers = Mock()
                    handler.wfile = Mock()
                    target = handler.end_headers if stage == "end_headers" else handler.wfile.write
                    target.side_effect = error("client disconnected")
                    with patch("sys.stderr") as stderr:
                        handler.reply(200, {"results": []})
                    stderr.write.assert_not_called()


class ModelTests(unittest.TestCase):
    def test_download_is_explicit(self):
        calls = []
        hub = types.SimpleNamespace(snapshot_download=lambda *a, **kw: calls.append((a, kw)))
        with patch.dict("sys.modules", huggingface_hub=hub):
            runtime.download("/private/cache")
        self.assertEqual(calls[0][0], (runtime.MODEL,))
        self.assertEqual(calls[0][1]["cache_dir"], "/private/cache")
        self.assertIn("*.safetensors", calls[0][1]["allow_patterns"])

    def test_loading_is_offline_and_scores_yes_no(self):
        calls = []
        class Tensor:
            def __getitem__(self, key):
                calls.append(("select", key))
                return self
            def float(self): return self
            def item(self): return 0.8
        class Inputs(dict):
            def to(self, device): return self
        class Tokenizer:
            def convert_tokens_to_ids(self, text): return {"no": 17, "yes": 42}[text]
            def encode(self, text, **kwargs):
                calls.append(("encode", text, kwargs))
                return [1, 2]
            def pad(self, inputs, **kwargs):
                calls.append(("pad", inputs, kwargs))
                return Inputs(inputs)
        class Model:
            def to(self, device): return self
            def eval(self): return self
            def model(self, **kwargs):
                calls.append(("forward", kwargs))
                return types.SimpleNamespace(last_hidden_state=Tensor())
            def lm_head(self, hidden): return Tensor()
        class Factory:
            def __init__(self, value): self.value = value
            def from_pretrained(self, *args, **kwargs):
                calls.append(("load", args, kwargs))
                return self.value
        torch = types.SimpleNamespace(
            cuda=types.SimpleNamespace(is_available=lambda: False),
            backends=types.SimpleNamespace(mps=types.SimpleNamespace(is_available=lambda: False)),
            float32="float32", inference_mode=lambda: lambda f: f,
            softmax=lambda t, dim: t)
        transformers = types.SimpleNamespace(AutoTokenizer=Factory(Tokenizer()), AutoModelForCausalLM=Factory(Model()))
        with patch.dict("sys.modules", torch=torch, transformers=transformers):
            score = runtime.load_scorer("/cache")
            results = score("find my preference", ["memory a", "memory b"])
        # A final slow document must not return stale scores after the budget;
        # neither may another document start after it expires.
        for documents in (["memory a"], ["memory a", "memory b"]):
            with patch.dict("sys.modules", torch=torch, transformers=transformers):
                with patch.object(runtime.time, "monotonic", side_effect=[0, 0, runtime.INFERENCE_SECONDS + 1]):
                    with self.assertRaises(TimeoutError):
                        score("find my preference", documents)
        self.assertEqual([r["relevance_score"] for r in results], [0.8, 0.8])
        loads = [c for c in calls if c[0] == "load"]
        self.assertEqual(len(loads), 2)
        self.assertTrue(all(c[2]["local_files_only"] for c in loads))
        self.assertTrue(all(c[2]["cache_dir"] == "/cache" for c in loads))
        self.assertEqual(loads[0][2]["padding_side"], "left")
        self.assertIn(("select", (0, [17, 42])), calls)
        forward = [c for c in calls if c[0] == "forward"]
        self.assertTrue(all(c[1]["use_cache"] is False for c in forward))
        self.assertTrue(any(c[0] == "encode" and "<Query>: find my preference" in c[1] for c in calls))


if __name__ == "__main__":
    unittest.main()
