FROM golang:1.26.4-alpine3.23 AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/dokploy-migrator ./cmd/dokploy-migrator

FROM alpine:3.23.5
RUN apk add --no-cache ca-certificates curl \
    && adduser -D -H migrator \
    && mkdir -p /data \
    && chown migrator:migrator /data
USER migrator
WORKDIR /app
COPY --from=build /out/dokploy-migrator /usr/local/bin/dokploy-migrator
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["dokploy-migrator"]
CMD ["serve"]
