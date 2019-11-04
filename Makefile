GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOMOD := $(GOCMD) mod
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
#VERSION := $(shell git describe --tags)
#BUILD := $(shell git rev-parse --short HEAD)
PROJECTNAME := $(shell basename "$(PWD)")

.DEFAULT_GOAL := build

.PHONY: all build install clean test vendor

all: install clean

build: vendor
	mkdir -p target/bin/
	$(GOBUILD) -mod vendor -v -o target/bin/ ./...
	mkdir -p target/conf/
	cp -af conf/* target/conf/
	# build ok

install: build
	umask 022
	[[ `uname` == "Linux" ]]

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

clean:
	rm -rf target/
	rm -rf vendor/
	$(GOCLEAN) -cache -testcache -modcache ./...
	# clean ok

test: vendor
	$(GOTEST) -mod vendor -v ./...
	# test ok

vendor:
	$(GOMOD) verify
	$(GOMOD) vendor
	# vendor ok
