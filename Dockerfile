
FROM golang:alpine  as builder

RUN apk --no-cache add ca-certificates && apk update && apk add glide git

WORKDIR /go/src/github.com/amcleodca/gcr-notifier
ADD . .

RUN go build -o bin/gcr-notifier .

FROM alpine 

WORKDIR /
RUN apk --no-cache add ca-certificates
COPY --from=builder /go/src/github.com/amcleodca/gcr-notifier/bin /app
