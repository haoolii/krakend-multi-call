FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /fab-settings-mock .

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
COPY --from=builder /fab-settings-mock /usr/local/bin/fab-settings-mock
USER app
EXPOSE 8080
ENTRYPOINT ["fab-settings-mock"]
