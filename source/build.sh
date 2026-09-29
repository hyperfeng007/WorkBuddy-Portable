#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")"
go mod download
go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-H=windowsgui -s -w' -o ../WorkBuddy-Portable.exe .
