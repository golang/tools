#!/bin/bash -e
RACE=false
if [[ "${1}" == "-race" ]]; then
  RACE=true
fi

echo -e "\\033[92m ---> currrent cmd/gopls \\033[0m"
find "$(command -v gopls)" -printf "%c %p\\n"

echo -e "\\033[92m ---> testing internal/lsp (race=${RACE}) \\033[0m"
if ${RACE}; then
    go test -race "$(pwd)"/internal/lsp/...
else
    go test "$(pwd)"/internal/lsp/...
fi

if ${RACE}; then
    go install -a -race "$(pwd)"/cmd/gopls
else
    go install -a "$(pwd)"/cmd/gopls
fi

echo -e "\\033[92m ---> installed cmd/gopls (race=${RACE}) \\033[0m"
find "$(command -v gopls)" -printf "%c %p\\n"
