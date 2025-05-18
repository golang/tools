#!/bin/sh

# Build README.md from the files in this directory.
# Must be invoked from the internal/cmp directory.

cd internal/readme

outfile=../../README.md

# Compile all the code used in the README.
go build -o /tmp/mcp-readme/ ./...
# Combine the code with the text in README.src.md.
# TODO: when at Go 1.24, use a tool directive for weave.
go run golang.org/x/example/internal/cmd/weave@latest README.src.md > $outfile

if [[ $1 = '-preview' ]]; then
	# Preview the README on GitHub.
	# $HOME/markdown must be a github repo.
	# Visit https://github.com/$HOME/markdown to see the result.
	cp $outfile $HOME/markdown/
	(cd $HOME/markdown/ && git commit -m . README.md && git push)
fi
