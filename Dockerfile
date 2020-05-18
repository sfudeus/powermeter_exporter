ARG GO_VERSION=1.14
FROM golang:$GO_VERSION AS builder
RUN mkdir /build
COPY *.go go.* /build/
WORKDIR /build
RUN CGO_ENABLED=0 go build -o powermeter_exporter

FROM scratch
COPY --from=builder /build/powermeter_exporter /
ENTRYPOINT [ "/powermeter_exporter" ]

