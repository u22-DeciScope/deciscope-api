FROM golang:1.25-alpine AS build

ARG BUILD_VERSION
ARG GIT_COMMIT_SHA
ARG BUILD_TIMESTAMP
ARG DIRTY_BUILD
ARG RELEASE_BUILD=false

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Release builds must carry an auditable identity; local builds fall back to the
# "dev"/"unknown" defaults handled in internal/app/build_info.go.
RUN if [ "${RELEASE_BUILD}" = "true" ]; then \
        test -n "${BUILD_VERSION}" \
        && test "${BUILD_VERSION}" != "dev" \
        && test "${BUILD_VERSION}" != "unknown" \
        && echo "${GIT_COMMIT_SHA}" | grep -Eq '^[0-9a-fA-F]{7,64}$' \
        && echo "${BUILD_TIMESTAMP}" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' \
        && echo "${DIRTY_BUILD}" | grep -Eq '^(true|false)$'; \
    fi
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X=deciscope-core-api/internal/app.binaryVersion=${BUILD_VERSION} -X=deciscope-core-api/internal/app.gitCommitSHA=${GIT_COMMIT_SHA} -X=deciscope-core-api/internal/app.buildTimestamp=${BUILD_TIMESTAMP} -X=deciscope-core-api/internal/app.dirtyBuild=${DIRTY_BUILD}" \
    -o /out/deciscope-api .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S deciscope \
    && adduser -S -G deciscope deciscope

WORKDIR /app
COPY --from=build /out/deciscope-api /app/deciscope-api

RUN chown -R deciscope:deciscope /app

USER deciscope
ENV PORT=9090
EXPOSE 9090

CMD ["/app/deciscope-api", "serve"]
