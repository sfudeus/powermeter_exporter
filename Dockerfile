FROM --platform=$BUILDPLATFORM golang:1.21 AS builder

ARG TARGETOS
ARG TARGETARCH

RUN mkdir /build
COPY *.go go.* /build/
WORKDIR /build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o powermeter_exporter

FROM scratch
COPY --from=builder /build/powermeter_exporter /
ENTRYPOINT [ "/powermeter_exporter" ]
EXPOSE 8080
