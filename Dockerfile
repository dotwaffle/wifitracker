FROM golang:alpine
LABEL maintainer "matthew@walster.org"
RUN mkdir -p /go/src/wifitracker
COPY . /go/src/wifitracker
RUN apk add --no-cache --virtual .build-deps \
	git \
	gcc \
	musl-dev \
	&& cd /go/src/wifitracker \
	&& go get -u -x github.com/golang/dep/... \
	&& dep ensure -v \
	&& go install -v \
	&& apk del .build-deps
VOLUME /db
WORKDIR /db
ENTRYPOINT /go/bin/wifitracker
