#!/usr/bin/env bash -e

if [[ "$(uname)" != "Linux" ]]
then
	echo operating system is not Linux
	exit 1
fi

umask 022
cd $(dirname "$0")

wget -O /tmp/simult.tar.gz "http://github.com/simult/simult/releases/latest/download/simult-$(uname -m).tar.gz"
tar -C /usr/local/bin -xvzf /tmp/simult.tar.gz

useradd -U -r -p* -d /etc/simult -m -s /bin/false simult

mkdir /var/log/simult/
chown simult: /var/log/simult/

mkdir -p /etc/simult/ssl/
chown -R simult: /etc/simult/

cp simult-server.service /etc/systemd/system/
systemctl daemon-reload
