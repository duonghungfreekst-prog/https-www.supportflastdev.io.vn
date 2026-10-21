@echo off
echo ==> Cross-compiling SupportFlast Engine for Linux (amd64)...
if not exist "bin" mkdir "bin"
cd supportflast_engine
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build -ldflags="-s -w" -o ..\bin\supportflast-linux-amd64 .
cd ..
echo ==> Linux binary built successfully: bin\supportflast-linux-amd64
