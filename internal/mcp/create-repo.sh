#!/bin/bash

# This script creates an MCP SDK repo from x/tools/internal/mcp (and friends).
# It will be used as a one-off to create github.com/modelcontextprotocol/go-sdk.
#
# Requires https://github.com/newren/git-filter-repo.

set -eu

# Check if exactly one argument is provided
if [ "$#" -ne 1 ]; then
  echo "create-repo.sh: create a standalone mcp SDK repo from x/tools"
  echo "Usage: $0 <mcp repo destination>"
  exit 1
fi >&2

src=$(go list -m -f {{.Dir}} golang.org/x/tools)
dest="$1"

echo "Filtering MCP commits from ${src} to ${dest}..." >&2

startdir=$(pwd)
tempdir=$(mktemp -d)
function cleanup {
  echo "cleaning up ${tempdir}"
  rm -rf "$tempdir"
} >&2
trap cleanup EXIT SIGINT

echo "Checking out to ${tempdir}"

git clone --bare "${src}" "${tempdir}"
git -C "${tempdir}" --git-dir=. filter-repo \
  --path internal/mcp/jsonschema --path-rename internal/mcp/jsonschema:jsonschema \
  --path internal/mcp/design --path-rename internal/mcp/design:design \
  --path internal/mcp/examples --path-rename internal/mcp/examples:examples \
  --path internal/mcp/internal --path-rename internal/mcp/internal:internal \
  --path internal/mcp/README.md --path-rename internal/mcp/README.md:README.md \
  --path internal/mcp/CONTRIBUTING.md --path-rename internal/mcp/CONTRIBUTING.md:CONTRIBUTING.md \
  --path internal/mcp --path-rename internal/mcp:mcp \
  --path internal/jsonrpc2_v2 --path-rename internal/jsonrpc2_v2:internal/jsonrpc2 \
  --path internal/xcontext \
  --replace-text "${startdir}/mcp-repo-replace.txt" \
  --force
mkdir ${dest}
cd "${dest}"
git init

cat << EOF > LICENSE
MIT License

Copyright (c) 2025 Go MCP SDK Authors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
EOF

git add LICENSE && git commit -m "Initial commit: add LICENSE"
git remote add filtered_source "${tempdir}"
git pull filtered_source master --allow-unrelated-histories
git remote remove filtered_source
go mod init github.com/modelcontextprotocol/go-sdk && go get go@1.23.0
go mod tidy
git add go.mod go.sum
git commit -m "all: add go.mod and go.sum file"
