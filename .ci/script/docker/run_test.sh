#!/usr/bin/env bash
set -e

docker build --rm -f ".ci/Dockerfile" -t code-go-langserver-ci .ci


docker run  \
       --rm \
       -v $PWD:/go/src/golang.org/x/tools code-go-langserver-ci \
       /bin/bash -c "set -ex
            go test ./internal/lsp
       "
