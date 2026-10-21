#!/bin/sh
set -e
echo "==> Cross-compiling SupportFlast Engine for Linux (amd64)..."
mkdir -p bin
cd supportflast_engine
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ../bin/supportflast-linux-amd64 .
cd ..
echo "==> Linux binary built successfully: bin/supportflast-linux-amd64"