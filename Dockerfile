FROM golang:alpine  as builder

RUN apk --no-cache add ca-certificates && apk update && apk add git make

RUN go get github.com/golang/dep && \
    cd $GOPATH/src/github.com/golang/dep && \
    go install ./...

WORKDIR /go/src/github.com/amcleodca/gcr-notifier
ADD . .

RUN make deps
RUN make build

FROM alpine 

WORKDIR /
RUN apk --no-cache add ca-certificates
COPY --from=builder /go/src/github.com/amcleodca/gcr-notifier/bin /app
