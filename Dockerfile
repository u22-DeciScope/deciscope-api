FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/deciscope-api .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S deciscope \
    && adduser -S -G deciscope deciscope

WORKDIR /app
COPY --from=build /out/deciscope-api /app/deciscope-api
COPY fixtures /app/fixtures

RUN mkdir -p /app/uploads && chown -R deciscope:deciscope /app

USER deciscope
ENV PORT=9090
ENV FIXTURE_DIR=/app/fixtures/meetings
ENV UPLOAD_DIR=/app/uploads
EXPOSE 9090

CMD ["/app/deciscope-api", "serve"]
