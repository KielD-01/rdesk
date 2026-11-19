#!/bin/sh
# Cross-compiles the Windows host (bin/rdesk.exe) on Linux with MinGW-w64 in Docker.
# Static: the .exe only imports KERNEL32.dll and msvcrt.dll.
set -eu
cd "$(dirname "$0")/.."
mkdir -p bin
docker run --rm -v "$PWD":/src -w /src golang:1.27-bookworm sh -ec '
  apt-get update -qq >/dev/null
  apt-get install -y -qq gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64 >/dev/null
  CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ \
    go build -buildvcs=false -trimpath -ldflags="-s -w -H windowsgui -extldflags=-static" -o bin/rdesk.exe ./cmd/rdesk
  chown '"$(id -u):$(id -g)"' bin/rdesk.exe'
ls -la bin/rdesk.exe
