# The binary is cross-compiled on the host by test-infra:milo-shim-image rather
# than built here: a second Go build stage would re-download the module cache
# the operator image already has warm, for a package of one file.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY milo-shim /milo-shim
USER 65532:65532
ENTRYPOINT ["/milo-shim"]
