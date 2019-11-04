#!/usr/bin/env bash

set -e
umask 022

os=$(uname | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
if [[ "$os" != "linux" ]]
then
	echo Operating system is not installable
	exit 1
fi
echo "OS: $os-$arch"

rm -rf /tmp/simult
rm -rf /tmp/simult.tar.gz
url="https://github.com/simult/simult/releases/latest/download/simult-$os-$arch.tar.gz"
if [[ "$1" != "" ]]
then
	url="https://github.com/simult/simult/releases/download/$1/simult-$os-$arch.tar.gz"
fi
wget -q -O /tmp/simult.tar.gz "$url"
mkdir /tmp/simult
tar -C /tmp/simult -xvzf /tmp/simult.tar.gz

useradd -U -r -p* -d /etc/simult -M -s /bin/false simult || true

chown simult: /tmp/simult/bin/*

cp -df --preserve=ownership /tmp/simult/bin/* /usr/local/bin/

mkdir -p /var/log/simult/
chown simult: /var/log/simult/

cp -df /tmp/simult/conf/logrotate /etc/logrotate.d/simult
chown root: /etc/logrotate.d/simult

mkdir -p /etc/simult/
mkdir -p /etc/simult/ssl/
cp -dn /tmp/simult/conf/server.yaml /etc/simult/
chown -R simult: /etc/simult/

cp -df /tmp/simult/conf/simult-server.service /etc/systemd/system/
chown root: /etc/systemd/system/simult-server.service
systemctl daemon-reload

rm -rf /tmp/simult
rm -rf /tmp/simult.tar.gz

echo Installed simult
