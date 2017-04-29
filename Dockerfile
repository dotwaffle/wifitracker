FROM golang:alpine
LABEL maintainer "matthew@walster.org"
RUN mkdir -p /go/src/wifitracker
COPY . /go/src/wifitracker
RUN apk add --no-cache --virtual .build-deps git \
	&& go get -u -x github.com/golang/dep/... \
	&& cd /go/src/wifitracker \
	&& dep ensure -v \
	&& go install -v \
	&& apk del .build-deps
ENTRYPOINT /go/bin/wifitracker
