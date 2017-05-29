FROM golang:alpine as build
MAINTAINER matthew@walster.org
LABEL maintainer "matthew@walster.org"
RUN mkdir -p /go/src/wifitracker
COPY . /go/src/wifitracker
RUN apk add --no-cache git
RUN go get -d -x github.com/golang/dep/... \
	&& cd /go/src/github.com/golang/dep/cmd/dep/ \
	&& go install -v
RUN cd /go/src/wifitracker/ \
	&& dep ensure -v \
	&& go install -v

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=build /go/bin/wifitracker .
CMD ["./wifitracker"]
