FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN go build -o /out/grok-bridge ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/grok-bridge /usr/local/bin/grok-bridge
EXPOSE 8080
ENTRYPOINT ["grok-bridge"]
CMD ["-config", "/config/config.yaml"]
