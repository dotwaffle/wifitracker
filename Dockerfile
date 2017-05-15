FROM golang:alpine
MAINTAINER matthew@walster.org
LABEL maintainer "matthew@walster.org"
RUN mkdir -p /go/src/wifitracker
COPY . /go/src/wifitracker
RUN apk add --no-cache --virtual .build-deps git \
	&& go get -d -x github.com/golang/dep/... \
	&& cd /go/src/github.com/golang/dep/cmd/dep/ \
	&& go install -v \
	&& cd /go/src/wifitracker/ \
	&& dep ensure -v \
	&& go install -v \
	&& cd /go/bin/ \
	&& rm -rf /go/src/ /go/pkg/ /go/bin/dep \
	&& apk del .build-deps
ENTRYPOINT /go/bin/wifitracker
