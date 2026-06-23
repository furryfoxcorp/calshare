# Build a fully static binary, then ship it on a minimal distroless base.
FROM golang:1.26 AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X github.com/furryfoxcorp/calshare/internal/version.Version=${VERSION}" \
    -o /out/caldav-share ./cmd/caldav-share

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/caldav-share /usr/local/bin/caldav-share

# The database lives here; mount a volume over it.
ENV CALDAV_DB_PATH=/data/db.sqlite
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/caldav-share"]
CMD ["serve"]
