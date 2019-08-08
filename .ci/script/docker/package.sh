#!/usr/bin/env bash
set -e


TAG_NAME=$(git rev-parse --short HEAD)

if [[ $# -eq 0 ]]; then
    echo "deploy snapshot package.."
    KIBANA_VERSION=8.0.0
    DESTINATION=snapshot/

elif [[ $# -eq 2 ]]; then
    echo "deploy release package.."
    echo "plugin version: $1"
    echo "Kibana version: $2"
    VERSION=$1
    KIBANA_VERSION=$2
    DESTINATION=release/
    TAG_NAME=$1-$TAG_NAME
    CMD="jq '.version=\"$VERSION\"' package.json > tmp && mv tmp package.json"
else
    echo "Wrong number of parameters!"
    exit 2
fi

docker build --rm -f ".ci/DockerfilePackage" --build-arg CI_USER_UID=$(id -u) -t code-lsp-go-langserver-package:latest .ci

KIBANA_MOUNT_ARGUMENT=""
if [[ -n $KIBANA_MOUNT ]]; then
    if [[ -d $KIBANA_MOUNT ]]; then
        echo "KIBANA_MOUNT '$KIBANA_MOUNT' will be used as the kibana source for the build."
    else
        echo "KIBANA_MOUNT '$KIBANA_MOUNT' is not a directory, aborting."
        exit 1
    fi
else
  # if the Kibana source repo is not set as KIBANA_MOUNT, we clone the repo
  echo "===> Cloning Kibana v$KIBANA_VERSION"
  git clone --depth 1 -b master https://github.com/elastic/kibana.git "$(pwd)/kibana"
  KIBANA_MOUNT="$(pwd)/kibana"
fi


ABSOLUTE_KIBANA_MOUNT=$(realpath "$KIBANA_MOUNT")
KIBANA_MOUNT_ARGUMENT=-v\ "$ABSOLUTE_KIBANA_MOUNT:/plugin/kibana:rw"

docker run \
        --rm -t $(tty &>/dev/null && echo "-i") \
        --user $(id -u):ciagent \
        -v "$PWD:/plugin/kibana-extra/go-langserver:rw" \
        --mount source=m2-vol,destination=/home/ciagent/.m2 \
        $KIBANA_MOUNT_ARGUMENT \
        code-lsp-go-langserver-package \
        /bin/bash -c "set -ex

                      # if the kibana repo is mounted from disk run the yarn
                      # commands as the node user to prepare it for the build
                      if test -n '$KIBANA_MOUNT_ARGUMENT'; then
                      (
                        cd /plugin/kibana
                        yarn kbn bootstrap
                        yarn add git-hash-package
                      )
                      fi

                      # fail fast if required kibana files are missing
                      for file in /plugin/kibana/node-modules/git-hash-package/index.js /plugin/kibana/packages/kbn-plugin-helpers/bin/plugin-helpers.js; do
                        if ! test -f\$file; then
                            echo \"Missing required \$file, aborting.\"
                            exit i
                        fi
                      done

                      $CMD

                      /plugin/kibana/node_modules/git-hash-package/index.js
                      jq '.version=\"\\(.version)-linux\"' package.json > package-linux.json
                      jq '.version=\"\\(.version)-darwin\"' package.json > package-darwin.json
                      jq '.version=\"\\(.version)-windows\"' package.json > package-windows.json
                      mkdir packages
                      for PLATFORM in linux darwin windows
                      do
                        curl -OL https://github.com/elastic/go-langserver/releases/download/$TAG_NAME/go-langserver-\$PLATFORM-amd64.tar.gz
                        mkdir lib
                        tar -xzf go-langserver-\$PLATFORM-amd64.tar.gz -C ./lib
                        mv package-\$PLATFORM.json package.json
                        echo $KIBANA_VERSION | /plugin/kibana/packages/kbn-plugin-helpers/bin/plugin-helpers.js build
                        mv build/go-langserver*.zip packages
                        [ -e ./lib ] && rm -rf ./lib
                      done"

ls ./packages
