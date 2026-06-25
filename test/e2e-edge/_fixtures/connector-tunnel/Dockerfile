# CONNECT-proxy stand-in for the connector tunnel.
# Single static binary, no dependencies beyond the standard library.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod .
COPY main.go .
# Standard library only; go.mod just gives the build a module context.
RUN CGO_ENABLED=0 go build -o /connect-proxy .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /connect-proxy /connect-proxy
USER nonroot:nonroot
ENTRYPOINT ["/connect-proxy"]
