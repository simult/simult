#!/usr/bin/env bash

set -e
cd $(dirname "$0")
umask 022

os=$(uname | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)

TARGETDIR=`pwd`/target

rm -rf $TARGETDIR
mkdir -p $TARGETDIR/bin
docker build -t simult-server:build4linux ..
docker run -v "$TARGETDIR/bin":/target -it --rm  --entrypoint rsync simult-server:build4linux -a /app/ /target/
echo Copied binaries to $TARGETDIR/bin

mkdir -p $TARGETDIR/conf
cp -a -f conf/* $TARGETDIR/conf
echo Copied configurations to $TARGETDIR/conf

tarflags="--owner=0 --group=0"
if [[ "$os" == "darwin" ]]
then
	tarflags="--uid=0 --gid=0"
fi
tar $tarflags -C target/ -cvzf $TARGETDIR/simult-linux-$arch.tar.gz bin conf
echo Archived to $TARGETDIR/simult-linux-$arch.tar.gz
