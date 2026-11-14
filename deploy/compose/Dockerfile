# Builds the tollgate and fakeprovider binaries for the demo stack.
# The release image of M8 is built by GoReleaser, not by this file.
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/tollgate ./cmd/fakeprovider

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/tollgate /out/fakeprovider /usr/local/bin/
USER nonroot
ENTRYPOINT ["tollgate"]
