FROM golang:1.13 AS builder
RUN mkdir /build
COPY *.go go.* /build/
WORKDIR /build
RUN CGO_ENABLED=0 GOOS=linux go build -o powermeter_exporter

FROM scratch
COPY --from=builder /build/powermeter_exporter /
ENTRYPOINT [ "/powermeter_exporter" ]

