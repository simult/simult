#!/usr/bin/env bash

cd $(dirname "$0")
TARGETDIR=`pwd`/target

docker build -t simult-server:build4linux ..
docker run -v "$TARGETDIR":/target -it --rm  --entrypoint rsync simult-server:build4linux -av /app/ /target/
