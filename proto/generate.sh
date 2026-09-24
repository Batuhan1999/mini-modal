#!/bin/sh

set -eu

protoc_gen_go="$(go tool -n protoc-gen-go)"
protoc_gen_go_grpc="$(go tool -n protoc-gen-go-grpc)"

protoc \
  -I=. \
  --plugin="protoc-gen-go=${protoc_gen_go}" \
  --plugin="protoc-gen-go-grpc=${protoc_gen_go_grpc}" \
  --go_out=.. \
  --go_opt=module=mini-modal \
  --go-grpc_out=.. \
  --go-grpc_opt=module=mini-modal \
  agent/v1/agent.proto
