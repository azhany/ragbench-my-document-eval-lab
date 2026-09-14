#!/usr/bin/env sh
set -eu

# Shell launcher for the local RAGbench-MY stack.
# Builds and starts all services in the foreground; Ctrl-C stops the stack.
cd "$(dirname "$0")/.."

docker compose up --build
