package main // import "github.com/sfudeus/powermeter_exporter"

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jacobsa/go-serial/serial"
	"github.com/jessevdk/go-flags"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

var port io.ReadWriteCloser

var options struct {
	Port                     int64  `long:"port" default:"8080" description:"The address to listen on for HTTP requests." env:"EXPORTER_PORT"`
	Interval                 int64  `long:"interval" default:"60" env:"INTERVAL" description:"The frequency in seconds in which to gather data"`
	Device                   string `long:"device" default:"/dev/irmeter0" description:"The device to read on"`
	MeterName                string `long:"metername" description:"The name of your meter, to uniquely name them if you have multiple"`
	Factor                   int64  `long:"factor" description:"Reduction factor for all readings" default:"1"`
	MaxValue                 int64  `long:"maxValue" description:"Maximum value for readings, to prevent overflows" default:"10000000"`
	Debug                    bool   `long:"debug" description:"Activate debug mode"`
	KeepAlive                bool   `long:"keepalive" description:"When true, keep tty connection open between reads"`
	MqttHost                 string `long:"mqttHost" description:"MQTT host to send data to (optional)"`
	MqttPort                 int64  `long:"mqttPort" description:"MQTT port to send data to (optional)" default:"1883"`
	MqttTls                  bool   `long:"mqttTls" description:"Activate TLS for MQTT"`
	MqttTlsInsecure          bool   `long:"mqttTlsInsecure" description:"Allow insecure TLS for MQTT"`
	MqttTopicPrefix          string `long:"mqttTopicPrefix" description:"Topic prefix for MQTT" default:"powermeter"`
	MqttDiscoveryTopicPrefix string `long:"mqttDiscoveryTopicPrefix" description:"Topic prefix for homeassistant discovery" default:"homeassistant"`
	MqttUser                 string `long:"mqttUser" description:"Username to use for the MQTT connection" env:"MQTT_USER"`
	MqttPassword             string `long:"mqttPassword" description:"Password to use for the MQTT connection" env:"MQTT_PASSWORD"`
}

var (
	gaugeReading = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "powermeter",
		Name:      "reading",
		Help:      "Current meter reading for consumed energy (unit depends on OBIS id)",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
			//obis id of the meter, like 1.8.1 for consumed electrical energy, first tariff
			"meter_id",
		})
	gatheringDuration = promauto.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: "powermeter",
		Name:      "gatheringduration",
		Help:      "The duration of data gatherings",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
		})
	connectionSetups = promauto.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: "powermeter",
		Name:      "connection_setup",
		Help:      "The duration of connection setups",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
		})
	connectionResets = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "powermeter",
		Name:      "connection_reset",
		Help:      "The number of connections resets",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
		})
)

type meterReading struct {
	name  string
	value float64
}

func main() {
	_, err := flags.Parse(&options)
	if err != nil {
		os.Exit(1)
	}

	if options.Debug {
		log.SetLevel(log.DebugLevel)
	}

	if len(options.MqttHost) > 0 {
		connectMqtt()
	}

	go func() {
		if options.KeepAlive {
			port = openConnection()
			defer closeConnection()
		}
		iteration := 0
		for {
			ok := gatherData(iteration)
			iteration++
			if !ok && options.KeepAlive {
				log.Printf("Data Gathering failed, resetting port")
				closeConnection()
				port = openConnection()
				connectionResets.WithLabelValues(options.MeterName).Inc()
			}
			time.Sleep(time.Duration(options.Interval) * time.Second)
		}
	}()
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", options.Port), nil))
}

func openConnection() io.ReadWriteCloser {
	timer := prometheus.NewTimer(connectionSetups.WithLabelValues(options.MeterName))
	defer timer.ObserveDuration()

	// Set up options.
	options := serial.OpenOptions{
		PortName:          options.Device,
		BaudRate:          9600,
		DataBits:          8,
		StopBits:          1,
		ParityMode:        serial.PARITY_NONE,
		RTSCTSFlowControl: false,
		MinimumReadSize:   16,
	}

	// Open the port.
	logDebug("Connecting serial port...")
	port, err := serial.Open(options)
	if err != nil {
		log.Fatalf("serial.Open: %v", err)
	}
	return port
}

func closeConnection() {
	logDebug("Closing serial port")
	port.Close()
}

func gatherData(iteration int) bool {
	timer := prometheus.NewTimer(gatheringDuration.WithLabelValues(options.MeterName))
	defer timer.ObserveDuration()

	if !options.KeepAlive {
		port = openConnection()
		defer closeConnection()
	}

	log.Println("Gathering metrics")
	message, err := readMessage(port)
	if err != nil {
		log.Printf("Failed to read message, skipping because of %v", err)
		return false
	}
	logDebug("Read full message\n%s\n", formatHexBytes(message, 32))

	smlListResponse, err := extractListResponse(message)
	if err != nil {
		log.Printf("Failed to extract list response from message, skipping: %v", err)
		return false
	}

	for _, meterReading := range extractMeterReadings(smlListResponse) {
		log.Printf("Recording meter %s with value %f", meterReading.name, meterReading.value)
		gaugeReading.WithLabelValues(options.MeterName, meterReading.name).Set(meterReading.value)
		publishData(meterReading, iteration)
	}
	return true
}
