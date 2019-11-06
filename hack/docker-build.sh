#!/usr/bin/env bash

set -e
umask 022

OS=$(uname | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | tr '[:upper:]' '[:lower:]')
echo "OS: $OS-$ARCH"

cd $(dirname "$0")
DIR=`pwd`

cd $DIR/../target/
TARGETDIR=`pwd`
cd $DIR

mkdir -p $TARGETDIR/
docker build -t simult-server:build4linux ..
docker run --rm -it -v "$TARGETDIR":/target --entrypoint rsync simult-server:build4linux -a /app/ /target/
echo Copied app to $TARGETDIR/

TARFLAGS="--owner=0 --group=0"
if [ "$OS" = "darwin" ]; then TARFLAGS="--uid=0 --gid=0"; fi
tar $TARFLAGS -C $TARGETDIR/ -cvzf $TARGETDIR/simult-linux-$ARCH.tar.gz bin conf
echo Archived to $TARGETDIR/simult-linux-$ARCH.tar.gz
