# Builds the tollgate and fakeprovider binaries for the demo stack.
# The release image of M8 is built by GoReleaser, not by this file.
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/tollgate ./cmd/fakeprovider

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/tollgate /out/fakeprovider /usr/local/bin/
USER nonroot
ENTRYPOINT ["tollgate"]
