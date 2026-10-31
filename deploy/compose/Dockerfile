# Builds the tollgate and fakeprovider binaries for the demo stack.
# The release image of M8 is built by GoReleaser, not by this file.
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/tollgate ./cmd/fakeprovider

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /out/tollgate /out/fakeprovider /usr/local/bin/
USER nonroot
ENTRYPOINT ["tollgate"]
