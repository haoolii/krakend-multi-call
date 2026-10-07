FROM golang:1.26.8-alpine3.23 AS plugin-builder

RUN apk add --no-cache gcc musl-dev binutils-gold

WORKDIR /src/backend-logger
COPY krakend/plugins/backend-logger/go.mod ./
RUN go mod download

COPY krakend/plugins/backend-logger/ ./
RUN CGO_ENABLED=1 go build -buildmode=plugin -o /out/backend-logger.so .

FROM krakend:2.13.11

COPY --from=plugin-builder /out/backend-logger.so /opt/krakend/plugins/backend-logger.so
