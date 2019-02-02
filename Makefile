linux: powermeter_exporter.linux
darwin: powermeter_exporter.darwin

powermeter_exporter.%: main.go
	GOOS=$* go build -o powermeter_exporter.$*
