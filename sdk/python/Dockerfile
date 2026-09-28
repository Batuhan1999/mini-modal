FROM python:3.12-slim

COPY pyproject.toml /tmp/mini-modal-sdk/pyproject.toml
COPY mini_modal /tmp/mini-modal-sdk/mini_modal

RUN python -m pip install --no-cache-dir /tmp/mini-modal-sdk \
    && rm -rf /tmp/mini-modal-sdk
