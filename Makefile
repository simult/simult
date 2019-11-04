GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOMOD := $(GOCMD) mod
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get

DOCKERCMD := docker

OS=$(shell uname | tr '[:upper:]' '[:lower:]')
ARCH=$(shell uname -m | tr '[:upper:]' '[:lower:]')

VERSION := $(shell git describe --tags)
BUILD := $(shell git rev-parse --short HEAD)
PROJECTNAME := $(shell basename "$(PWD)")

LDFLAGS:= -ldflags "-X=github.com/simult/simult/pkg/version.version=$(VERSION) -X=github.com/simult/simult/pkg/version.build=$(BUILD)"

.DEFAULT_GOAL := build

.PHONY: all build install clean test vendor

all: clean build

build: vendor
	mkdir -p target/bin/
	$(GOBUILD) $(LDFLAGS) -mod vendor -v -o target/bin/ ./...
	mkdir -p target/conf/
	cp -af conf/* target/conf/
	TARFLAGS="--owner=0 --group=0"
	if [ "$(OS)" == "darwin" ]; then TARFLAGS="--uid=0 --gid=0"; fi
	tar $$TARFLAGS -C target/ -cvzf target/simult-$(OS)-$(ARCH).tar.gz bin conf
	# build ok

install: build
	umask 022
	[ "$(OS)" == "linux" ]

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

docker-build:
	mkdir -p target/
	$(DOCKERCMD) run --rm -it -v "$(PWD)":/go/src/github.com/simult/simult -w /go/src/github.com/simult/simult golang:1.13-buster make build
	TARFLAGS="--owner=0 --group=0"
	if [ "$(OS)" == "darwin" ]; then TARFLAGS="--uid=0 --gid=0"; fi
	tar $$TARFLAGS -C target/ -cvzf target/simult-linux-$(ARCH).tar.gz bin conf
	# docker-build ok
