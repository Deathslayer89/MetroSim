# Builds one MetroSim command, picked with --build-arg BINARY=<directory under cmd/>.

FROM golang:1.25-alpine AS builder
RUN apk add --no-cache build-base git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG BINARY
RUN go build -trimpath -ldflags='-s -w' -o /out/app ./cmd/${BINARY}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 metrosim
USER metrosim
COPY --from=builder /out/app /app
ENTRYPOINT ["/app"]
