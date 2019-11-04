#!/usr/bin/env bash

set -e
umask 022

os=$(uname)
arch=$(uname -m)
echo "OS: $os-$arch"

if [[ "$os" != "Linux" ]]
then
	echo Operating system is not installable
	exit 1
fi

rm -rf /tmp/simult-install/
mkdir -p /tmp/simult-install/target/
cd /tmp/simult-install/

url="https://github.com/simult/simult/releases/latest/download/simult-$os-$arch.tar.gz"
if [[ "$1" != "" ]]
then
	url="https://github.com/simult/simult/releases/download/$1/simult-$os-$arch.tar.gz"
fi
wget -q -O simult.tar.gz "$url"
tar -C target/ -xvzf simult.tar.gz

	useradd -U -r -p* -d /etc/simult -M -s /bin/false simult || true

	chown simult: target/bin/*

	cp -df --preserve=ownership target/bin/* /usr/local/bin/

	mkdir -p /var/log/simult/
	chown simult: /var/log/simult/

	cp -df target/conf/logrotate /etc/logrotate.d/simult
	chown root: /etc/logrotate.d/simult

	mkdir -p /etc/simult/
	mkdir -p /etc/simult/ssl/
	cp -dn target/conf/server.yaml /etc/simult/
	chown -R simult: /etc/simult/

	cp -df target/conf/simult-server.service /etc/systemd/system/
	chown root: /etc/systemd/system/simult-server.service
	systemctl daemon-reload
	# install ok

cd
rm -rf /tmp/simult-install/

echo Installed simult
