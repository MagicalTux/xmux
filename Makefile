#!/bin/make
GOPATH:=$(shell go env GOPATH)

all:
	$(GOPATH)/bin/goimports -w -l .
	go build -v

.PHONY: test

test:
	go test


