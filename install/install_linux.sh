#!/usr/bin/env bash

set -e
cd $(dirname "$0")
umask 022

if [[ "$(uname)" != "Linux" ]]
then
	echo operating system is not Linux
	exit 1
fi

wget -O /tmp/simult.tar.gz "http://github.com/simult/simult/releases/latest/download/simult-$(uname -m).tar.gz"
tar -C /usr/local/bin -xvzf /tmp/simult.tar.gz

useradd -U -r -p* -d /etc/simult -M -s /bin/false simult || true

mkdir -p /var/log/simult/
chown simult: /var/log/simult/

cp -f logrotate /etc/logrotate.d/simult

mkdir -p /etc/simult/
mkdir -p /etc/simult/ssl/
cp -n server.yaml /etc/simult/
chown -R simult: /etc/simult/

cp -f simult-server.service /etc/systemd/system/
systemctl daemon-reload
