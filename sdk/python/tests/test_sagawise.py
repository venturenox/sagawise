"""Contract for the Python SDK (docs/contract.md, audit #15 and the cut list):
every call sends the HTTP request it describes, a failed request is raised to
the caller (never returned as a value), and timeouts are in a sane unit.

Run with `python -m pytest -q` from sdk/python.
"""
import json
import os
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

import pytest
import requests

from sagawise.sagawise import Sagawise


class _Handler(BaseHTTPRequestHandler):
    requests = []

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length).decode() if length else ""
        url = urlparse(self.path)
        _Handler.requests.append({"path": url.path, "query": parse_qs(url.query), "body": body})
        if "boom" in self.path:
            self.send_response(500)
            self.end_headers()
            self.wfile.write(b"boom")
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"workflow_instance_id": "abc123"}).encode())

    def log_message(self, *_):
        pass


@pytest.fixture(scope="module")
def server():
    srv = HTTPServer(("127.0.0.1", 0), _Handler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    os.environ["SAGAWISE_URL"] = f"http://127.0.0.1:{srv.server_port}"
    yield srv
    srv.shutdown()


@pytest.fixture
def sdk(server):
    _Handler.requests.clear()
    return Sagawise()


def last():
    return _Handler.requests[-1]


def test_start_workflow_sends_request_and_returns_id(sdk):
    assert sdk.start_workflow("order_flow", "1.0") == "abc123"
    assert last()["path"] == "/start_instance"
    assert last()["query"]["workflow_name"] == ["order_flow"]


def test_publish_with_default_is_retry_sends_request(sdk):
    sdk.publish_message("abc123", "1.0", "order_created", payload={"order_id": 1})
    r = last()
    assert r["path"] == "/update_instance"
    assert r["query"]["action_type"] == ["publish"]
    assert r["query"]["is_retry"][0].lower() == "false"
    assert json.loads(r["body"]) == {"order_id": 1}


def test_consume_and_fail_send_requests(sdk):
    sdk.consume_message("abc123", "1.0", "order_created", "payments")
    assert last()["query"]["action_type"] == ["consume"]
    assert last()["query"]["service_name"] == ["payments"]
    sdk.fail_message("abc123", "1.0", "order_created", "payments")
    assert last()["query"]["action_type"] == ["fail"]


def test_5xx_raises(sdk):
    with pytest.raises(requests.exceptions.HTTPError):
        sdk.consume_message("abc123", "1.0", "boom", "payments")


def test_unreachable_server_raises(server):
    os.environ["SAGAWISE_URL"] = "http://127.0.0.1:1"
    try:
        with pytest.raises(requests.exceptions.RequestException):
            Sagawise().start_workflow("x", "1.0")
    finally:
        os.environ["SAGAWISE_URL"] = f"http://127.0.0.1:{server.server_port}"


def test_default_timeout_is_seconds_not_ms():
    assert Sagawise().timeout <= 60


def test_missing_required_args_raise():
    with pytest.raises(ValueError):
        Sagawise().start_workflow("", "1.0")


def test_is_retry_is_sent_lowercase(sdk):
    sdk.consume_message("abc123", "1.0", "order_created", "payments", is_retry=True)
    assert last()["query"]["is_retry"] == ["true"]


def test_missing_args_send_nothing(sdk):
    with pytest.raises(ValueError):
        sdk.publish_message("abc123", "1.0", "order_created", payload=None)
    with pytest.raises(ValueError):
        sdk.consume_message("abc123", "1.0", "", "payments")
    assert _Handler.requests == []
