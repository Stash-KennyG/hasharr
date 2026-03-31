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
RUN mkdir -p /config
COPY --from=build /out/hasharr /usr/local/bin/hasharr
COPY resources /app/resources
RUN ffmpeg -y -v error -i /app/resources/favicon_source.png \
    -vf "scale=32:32:force_original_aspect_ratio=decrease,pad=32:32:(ow-iw)/2:(oh-ih)/2:color=0x00000000" \
    /app/resources/favicon.ico \
    && ffmpeg -y -v error -i /app/resources/favicon_source.png \
    -vf "scale=32:32:force_original_aspect_ratio=decrease,pad=32:32:(ow-iw)/2:(oh-ih)/2:color=0x00000000" \
    /app/resources/favicon-32.png

EXPOSE 9995
VOLUME ["/config"]
ENTRYPOINT ["/usr/local/bin/hasharr"]
