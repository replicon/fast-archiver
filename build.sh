#!/bin/bash

TAG=`git describe 2> /dev/null`
REV=`git rev-parse --short HEAD 2> /dev/null`

if [ -z "$TAG" ];
then
    TAG="-"
fi
if [ -z "$REV" ];
then
    REV="-"
fi

go build -ldflags "-X main.tag $TAG -X main.rev $REV"

