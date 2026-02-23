# This makefile builds ../README.md from the files in this directory.

OUTFILE=../../README.md

$(OUTFILE): build README.src.md
	go run golang.org/x/example/internal/cmd/weave@latest README.src.md > $(OUTFILE)

# Compile all the code used in the README.
build: $(wildcard */*.go)
	go build -o /tmp/mcp-readme/ ./...

# Preview the README on GitHub.
# $HOME/markdown must be a github repo.
# Visit https://github.com/$HOME/markdown to see the result.
preview: $(OUTFILE)
	cp $(OUTFILE) $$HOME/markdown/
	(cd $$HOME/markdown/ && git commit -m . README.md && git push)

.PHONY: build preview
