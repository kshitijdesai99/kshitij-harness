"""Private loopback Qwen reranker. Only --download may access the model hub.

Scoring follows https://huggingface.co/Qwen/Qwen3-Reranker-0.6B:
left-pad the instructional chat prompt, compare final-token yes/no logits.
"""
import json
import math
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = "Qwen/Qwen3-Reranker-0.6B"
MAX_BODY = 2 * 1024 * 1024
# Allow a burst of three full-budget workers: two queued, one inferring.
# Keep the Go client's 25s deadline above the combined 24s service budget.
LOCK_WAIT_SECONDS = 16
INFERENCE_SECONDS = 8


def download(cache):
    from huggingface_hub import snapshot_download
    snapshot_download(MODEL, cache_dir=cache,
                      allow_patterns=["*.json", "*.safetensors", "*.txt"])


def load_scorer(cache):
    import torch
    from transformers import AutoModelForCausalLM, AutoTokenizer
    tokenizer = AutoTokenizer.from_pretrained(
        MODEL, cache_dir=cache, local_files_only=True, padding_side="left")
    device = "cuda" if torch.cuda.is_available() else (
        "mps" if torch.backends.mps.is_available() else "cpu")
    model = AutoModelForCausalLM.from_pretrained(
        MODEL, cache_dir=cache, local_files_only=True,
        torch_dtype=torch.float32 if device == "cpu" else torch.float16
    ).to(device).eval()
    no, yes = [tokenizer.convert_tokens_to_ids(t) for t in ("no", "yes")]
    prefix = tokenizer.encode(
        '<|im_start|>system\nJudge whether the Document meets the requirements '
        'based on the Query and the Instruct provided. Note that the answer '
        'can only be "yes" or "no".<|im_end|>\n<|im_start|>user\n',
        add_special_tokens=False)
    suffix = tokenizer.encode(
        '<|im_end|>\n<|im_start|>assistant\n<think>\n\n</think>\n\n',
        add_special_tokens=False)

    @torch.inference_mode()
    def score(query, documents):
        results = []
        deadline = time.monotonic() + INFERENCE_SECONDS
        # Single-document batches bound memory even on CPU-only machines.
        for index, doc in enumerate(documents):
            if time.monotonic() > deadline:
                raise TimeoutError("reranking request exceeded its time budget")
            text = ("<Instruct>: Given a user request and a saved memory, determine "
                    "whether this memory is needed to fulfill that specific request. "
                    "Return yes for a directly matching topic or a saved instruction "
                    "that governs the requested action, including constraints on where "
                    "or how to perform it. Return no for unrelated instructions, "
                    "incidental word overlap, greetings, acknowledgments, and questions "
                    "outside the memory topic.\n<Query>: " + query + "\n<Document>: " + doc)
            ids = tokenizer.encode(text, add_special_tokens=False,
                                   truncation=True,
                                   max_length=8192-len(prefix)-len(suffix))
            inputs = tokenizer.pad({"input_ids": [prefix+ids+suffix]},
                                   padding=True, return_tensors="pt").to(device)
            # Compute only the final hidden position's vocabulary projection,
            # avoiding an 8192 x vocabulary logits allocation.
            hidden = model.model(**inputs, use_cache=False).last_hidden_state[:, -1, :]
            logits = model.lm_head(hidden).float()[0, [no, yes]]
            value = torch.softmax(logits, dim=-1)[1].item()
            if not math.isfinite(value):
                raise ValueError("non-finite model score")
            results.append({"index": index, "relevance_score": value})
        # An individual model call cannot be interrupted, so also reject a
        # final document that finishes after the bounded inference budget.
        if time.monotonic() > deadline:
            raise TimeoutError("reranking request exceeded its time budget")
        return sorted(results, key=lambda r: r["relevance_score"], reverse=True)
    return score


class Handler(BaseHTTPRequestHandler):
    scorer = None
    inference_lock = threading.Lock()

    def setup(self):
        super().setup()
        self.connection.settimeout(10)

    def log_message(self, *_):
        pass  # Never log queries or memory contents.

    def reply(self, status, data):
        body = json.dumps(data, allow_nan=False).encode()
        try:
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass  # Canceled clients must not generate server tracebacks.

    def do_GET(self):
        if self.path != "/health":
            return self.reply(404, {"error": "not found"})
        self.reply(200, {"model": MODEL, "ready": True})

    def do_POST(self):
        if self.path != "/rerank":
            return self.reply(404, {"error": "not found"})
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > MAX_BODY:
                return self.reply(413, {"error": "request size out of bounds"})
            data = json.loads(self.rfile.read(length))
            if not isinstance(data, dict):
                raise ValueError()
            query, docs = data.get("query"), data.get("documents")
            if (not isinstance(query, str) or not query.strip() or
                    not isinstance(docs, list) or not 1 <= len(docs) <= 128 or
                    any(not isinstance(d, str) for d in docs)):
                raise ValueError()
        except (ValueError, TypeError, UnicodeDecodeError):
            return self.reply(400, {"error": "expected query and 1..128 document strings"})
        if not self.inference_lock.acquire(timeout=LOCK_WAIT_SECONDS):
            return self.reply(503, {"error": "local reranker is busy"})
        try:
            try:
                results = self.scorer(query, docs)
            except Exception:
                return self.reply(500, {"error": "local reranker inference failed"})
            self.reply(200, {"model": MODEL, "results": results})
        finally:
            self.inference_lock.release()


if __name__ == "__main__":
    mode, cache = sys.argv[1:]
    if mode == "--download":
        download(cache)
    elif mode == "--serve":
        os.environ["HF_HUB_OFFLINE"] = "1"
        os.environ["TRANSFORMERS_OFFLINE"] = "1"
        # Bind before expensive loading: competing processes cannot load twice.
        server = ThreadingHTTPServer(("127.0.0.1", 11435), Handler)
        Handler.scorer = staticmethod(load_scorer(cache))
        server.serve_forever()
    else:
        raise SystemExit("unknown mode")
