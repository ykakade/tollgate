# Builds the tollgate and fakeprovider binaries for the demo stack.
# The release image of M8 is built by GoReleaser, not by this file.
FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/tollgate ./cmd/fakeprovider

FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
COPY --from=build /out/tollgate /out/fakeprovider /usr/local/bin/
USER nonroot
ENTRYPOINT ["tollgate"]
