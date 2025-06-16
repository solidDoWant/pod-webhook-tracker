ARG DEBIAN_IMAGE_VERSION=12.9-slim
FROM debian:${DEBIAN_IMAGE_VERSION}

# Install the tool
ARG TARGETOS
ARG TARGETARCH
ARG SOURCE_BINARY_PATH="build/binaries/${TARGETOS}/${TARGETARCH}/pod-webhook-tracker"
ARG SOURCE_LICENSE_PATH="build/licenses/"
COPY "${SOURCE_BINARY_PATH}" /bin/pod-webhook-tracker
COPY "${SOURCE_LICENSE_PATH}" /usr/share/doc/pod-webhook-tracker

# Configure runtime settings
USER 1000:1000
EXPOSE 8080
ENTRYPOINT [ "/bin/pod-webhook-tracker", "serve" ]
