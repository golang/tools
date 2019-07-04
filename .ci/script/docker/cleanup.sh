#!/usr/bin/env bash

docker stop $(docker ps -a -q)
docker rm -v $(docker ps -a -q)
docker volume prune -f

exit 0
