# Makefile cho hệ thống SupportFlast (supportflast.dev.io.vn)

.PHONY: all build build-linux test clean docker docker-multiarch run pqc-test

all: build

build:
	@echo "==> Building Rust Core (supportflast_core)..."
	cd supportflast_core && cargo build --release
	@echo "==> Building Go Engine (supportflast_engine)..."
	cd supportflast_engine && go build -o supportflast.exe .
	@echo "==> Build complete!"

build-linux:
	@echo "==> Cross-compiling Linux Binaries (amd64 & arm64)..."
	cmd /c build_linux.bat


test:
	@echo "==> Testing Rust Core..."
	cd supportflast_core && cargo test
	@echo "==> Testing Go Engine..."
	cd supportflast_engine && go test -v ./...
	@echo "==> Testing Python AI Subagents..."
	cd supportflast_ai && python test_agents.py
	@echo "==> All tests passed!"

pqc-test:
	@echo "==> Testing Rust Post-Quantum Cryptography (Kyber + Dilithium)..."
	cd supportflast_core && cargo test pqc

clean:
	@echo "==> Cleaning build artifacts..."
	cd supportflast_core && cargo clean
	rm -f supportflast_engine/supportflast.exe
	@echo "==> Clean complete!"

docker:
	@echo "==> Building Docker image for local architecture..."
	docker build -t supportflast:latest .

docker-multiarch:
	@echo "==> Building Multi-Arch Docker images (linux/amd64, linux/arm64)..."
	docker buildx build --platform linux/amd64,linux/arm64 -t supportflast:latest .

run:
	@echo "==> Starting SupportFlast Services (XAMPP Full System)..."
	cmd /c RUN_FULL_SYSTEM.bat

