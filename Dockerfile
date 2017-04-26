FROM golang:alpine
MAINTAINER matthew@walster.org
RUN apk add --no-cache --virtual .build-deps git gcc musl-dev \
	&& go get -u -x github.com/golang/dep/... \
	&& go get -d -u -x github.com/dotwaffle/wifitracker \
	&& cd /go/src/github.com/dotwaffle/wifitracker \
	&& dep ensure \
	&& go install -a -v \
	&& apk del .build-deps
ENTRYPOINT /go/bin/wifitracker
