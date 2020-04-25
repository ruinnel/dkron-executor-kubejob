FROM golang:1.14 as build-stage

RUN mkdir -p /app
WORKDIR /app

ENV GO111MODULE=on
COPY go.mod go.mod
COPY go.sum go.sum
COPY main.go main.go
COPY kubejob.go kubejob.go
RUN go mod download

RUN go build -o ./dkron-executor-kubejob

FROM dkron/dkron:v2.2.1 as dkron-image

COPY --from=build-stage /app/dkron-executor-kubejob /etc/dkron/plugins/dkron-executor-kubejob
