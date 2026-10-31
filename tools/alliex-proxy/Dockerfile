FROM golang:1.26.1-alpine AS build
WORKDIR /src
COPY go.mod *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /alliex-proxy .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /alliex-proxy /usr/local/bin/alliex-proxy
USER nobody
ENV LISTEN_ADDR=0.0.0.0:8787
ENTRYPOINT ["alliex-proxy"]
