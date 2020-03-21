# powermeter_exporter

A simple tool to extract powermeter readings from an electronic meter which can emit SML data, e.g. by using an IR device and have it scraped by Prometheus.

```
Usage:
  powermeter_exporter [OPTIONS]

Application Options:
      --port=      The address to listen on for HTTP requests. (default: 8080) [$EXPORTER_PORT]
      --interval=  The frequency in seconds in which to gather data (default: 60) [$INTERVAL]
      --device=    The device to read on (default: /dev/irmeter0)
      --metername= The name of your meter, to uniquely name them if you have multiple
      --factor=    Reduction factor for all readings (default: 1)
      --debug      Activate debug mode
      --keepalive  When true, keep tty connection open between reads

Help Options:
  -h, --help       Show this help message

```
