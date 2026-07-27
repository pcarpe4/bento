FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collector .

# A static binary on a scratch base: no shell, no package manager, nothing to
# patch. Runs as a non-root arbitrary UID, which is what OpenShift's
# restricted SCC assigns.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/collector /collector
USER 1001
ENTRYPOINT ["/collector"]
CMD ["-config", "/etc/collector/config.yaml"]
