# Makefile cho hệ thống SupportFlast (supportflast.dev.io.vn)
# Tuân thủ nghiêm ngặt Rule PHẦN 6 - Antigravity Polyglot Architecture

.PHONY: all build build-linux test clean docker docker-multiarch run pqc-test lint key-ceremony siem kafka

all: build

build:
	@echo "==> Building Rust Core (supportflast_core)..."
	cd supportflast_core && cargo build --release
	@echo "==> Building Go Engine (supportflast_engine)..."
	cd supportflast_engine && go build -o supportflast.exe .
	@echo "==> Build complete!"

build-linux:
	@echo "==> Cross-compiling Linux Binaries (amd64)..."
	cd supportflast_engine && go build -ldflags="-s -w" -o ../bin/supportflast-linux-amd64 .

test:
	@echo "==> Testing Rust Core..."
	cd supportflast_core && cargo test
	@echo "==> Testing Go Engine..."
	cd supportflast_engine && go test -v ./...
	@echo "==> Testing Python AI Subagents..."
	cd supportflast_ai && python test_agents.py
	@echo "==> All tests passed!"

lint:
	@echo "==> [LINT] Linting Go Engine (go vet)..."
	-cd supportflast_engine && go vet ./...
	@echo "==> [LINT] Linting Rust Core (cargo clippy)..."
	cd supportflast_core && cargo clippy --quiet
	@echo "==> [LINT] Lint complete!"

pqc-test:
	@echo "==> Testing Rust Post-Quantum Cryptography (Kyber + Dilithium)..."
	cd supportflast_core && cargo test pqc

key-ceremony:
	@echo "==> [KEY-CEREMONY] Testing RSA RS256 Key Ceremony & Verification (Go Engine)..."
	cd supportflast_engine && go test -v ./security -run "TestKeyGenerationAndPersistence|TestGenerateAndValidateRS256Token"
	@echo "==> [KEY-CEREMONY] Testing PQC Dilithium Signature Ceremony (Rust Core)..."
	cd supportflast_core && cargo test dilithium
	@echo "==> [KEY-CEREMONY] Key ceremony and cryptographic verification complete!"

siem:
	@echo "==> [SIEM] Testing SIEM Client & Honeypot (Go Engine)..."
	cd supportflast_engine && go test -v ./security -run "TestSIEM|TestHoneypot"
	@echo "==> [SIEM] Testing SIEM AI Threat Intelligence Engine (Python AI)..."
	cd supportflast_ai && python test_siem_engine.py
	@echo "==> [SIEM] Testing SIEM API Endpoints (FastAPI)..."
	cd supportflast_ai && python test_siem_api.py
	@echo "==> [SIEM] Security audit tests passed!"

kafka:
	@echo "==> [KAFKA] Testing Kafka/Redpanda Config & Idempotency Key (Go Engine)..."
	cd supportflast_engine && go test -v -run TestKafka
	@echo "==> [KAFKA] Testing Kafka Async Consumer & Idempotency Deduplication (Python AI)..."
	cd supportflast_ai && python test_kafka_idempotency.py
	@echo "==> [KAFKA] Kafka/Redpanda idempotency compliance verified!"

clean:
	@echo "==> Cleaning build artifacts..."
	cd supportflast_core && cargo clean
ifeq ($(OS),Windows_NT)
	-cmd /c "del /f /q supportflast_engine\supportflast.exe 2>nul"
	-rm -f supportflast_engine/supportflast.exe
else
	rm -f supportflast_engine/supportflast.exe bin/supportflast-linux-amd64
endif
	@echo "==> Clean complete!"

docker:
	@echo "==> Building Docker image for local architecture..."
	docker build -t supportflast:latest .

docker-multiarch:
	@echo "==> Building Multi-Arch Docker images (linux/amd64, linux/arm64)..."
	docker buildx build --platform linux/amd64,linux/arm64 -t supportflast:latest .

run:
ifeq ($(OS),Windows_NT)
	@echo "==> Starting SupportFlast Services (Windows)..."
	cmd /c RUN_FULL_SYSTEM.bat
else
	@echo "==> Starting SupportFlast Services (Linux)..."
	@if [ -f scripts/entrypoint.sh ]; then ./scripts/entrypoint.sh; else cd supportflast_engine && go run .; fi
endif
