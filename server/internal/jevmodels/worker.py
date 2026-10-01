# /// script
# requires-python = ">=3.11"
# dependencies = ["decider-ai[serve]==1.8.1"]
# ///
# Managed invocation: <daemon-cache>/engine-1.8.1/bin/python -I worker.py
# The manager creates the isolated environment only on explicit download.
"""Private, parent-owned wrapper for the pinned native SystemOne server."""

import hmac
import os
import sys
import threading

import uvicorn
from decider.serve import app
from starlette.requests import Request
from starlette.responses import JSONResponse, Response
from starlette.middleware.base import RequestResponseEndpoint


def watch_parent() -> None:
    """EOF means the owning daemon exited, including an ungraceful death."""
    sys.stdin.buffer.read()
    os._exit(0)


token = os.environ.pop("MULTICA_JEV_TOKEN")


@app.middleware("http")
async def authenticate(request: Request, call_next: RequestResponseEndpoint) -> Response:
    if not hmac.compare_digest(request.headers.get("authorization", ""), "Bearer " + token):
        return JSONResponse({"error": "unauthorized"}, status_code=401)
    return await call_next(request)


if __name__ == "__main__":
    threading.Thread(target=watch_parent, daemon=True).start()
    uvicorn.run(app, host="127.0.0.1", port=int(os.environ["MULTICA_JEV_PORT"]), access_log=False)
