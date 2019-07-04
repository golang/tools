#!/usr/bin/env bash
set -e

docker build --rm -f ".ci/Dockerfile" --build-arg CI_USER_ID=$(id -u) -t code-go-langserver-ci .ci


docker run  \
       --rm \
       -v $PWD/../tools/:/go/src/golang.org/x/tools code-go-langserver-ci \
       /bin/bash -c "set -ex
            go test ./internal/lsp
       "