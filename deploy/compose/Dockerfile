# Builds the tollgate and fakeprovider binaries for the demo stack.
# The release image of M8 is built by GoReleaser, not by this file.
FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/tollgate ./cmd/fakeprovider

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /out/tollgate /out/fakeprovider /usr/local/bin/
USER nonroot
ENTRYPOINT ["tollgate"]
