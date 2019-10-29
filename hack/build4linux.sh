#!/usr/bin/env bash

set -e
cd $(dirname "$0")
TARGETDIR=`pwd`/target

docker build -t simult-server:build4linux ..
docker run -v "$TARGETDIR":/target -it --rm  --entrypoint rsync simult-server:build4linux -a /app/ /target/
echo Copied binaries to $TARGETDIR
