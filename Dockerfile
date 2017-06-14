FROM golang:alpine as build
MAINTAINER matthew@walster.org
LABEL maintainer "matthew@walster.org"
RUN apk add --no-cache git
RUN go get -u -x github.com/golang/dep/...
RUN mkdir -p /go/src/wifitracker
WORKDIR /go/src/wifitracker/
COPY . /go/src/wifitracker
RUN dep ensure -v
RUN go install -v

FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /root/
COPY --from=build /go/bin/wifitracker .
ENTRYPOINT ["./wifitracker"]
