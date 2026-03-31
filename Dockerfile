FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/hasharr ./cmd/hasharr

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/hasharr /usr/local/bin/hasharr

EXPOSE 9995
ENTRYPOINT ["/usr/local/bin/hasharr"]
