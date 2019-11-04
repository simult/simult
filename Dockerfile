FROM golang:1.13-buster AS builder

WORKDIR /go/src/github.com/simult/simult
COPY . .

RUN make build
RUN mv target/ /app/

FROM debian:buster

ARG cmd=simult-server
ENV cmd=${cmd}

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        bash \
        rsync \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app/
COPY --from=builder /app/ ./

ENTRYPOINT ["bash", "-c", "exec /app/bin/${cmd} ${@:1}"]
