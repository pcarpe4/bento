FROM rust:1.94 AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
# Pre-build dependencies for layer caching.
RUN mkdir src && echo "fn main() {}" > src/main.rs && cargo build --release && rm -rf src
COPY src ./src
COPY tests ./tests
RUN touch src/main.rs && cargo build --release

# rustls means no OpenSSL; distroless/cc provides just glibc. Runs as a
# non-root arbitrary UID, which is what OpenShift's restricted SCC assigns.
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /src/target/release/collector /collector
USER 65532
ENTRYPOINT ["/collector"]
CMD ["-config", "/etc/collector/config.yaml"]
