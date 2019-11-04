#!/usr/bin/env bash

set -e
umask 022

os=$(uname)
arch=$(uname -m)
echo "OS: $os-$arch"

cd $(dirname "$0")
DIR=`pwd`

cd $DIR/../target/
TARGETDIR=`pwd`
cd $DIR

rm -rf $TARGETDIR/
mkdir -p $TARGETDIR/
docker build -t simult-server:build4linux ..
docker run -v "$TARGETDIR":/target -it --rm  --entrypoint rsync simult-server:build4linux -a /app/ /target/
echo Copied app to $TARGETDIR/

tarflags="--owner=0 --group=0"
if [[ "$os" == "Darwin" ]]
then
	tarflags="--uid=0 --gid=0"
fi
tar $tarflags -C $TARGETDIR/ -cvzf $TARGETDIR/simult-linux-$arch.tar.gz bin conf
echo Archived to $TARGETDIR/simult-linux-$arch.tar.gz
