package main // import "github.com/sfudeus/powermeter_exporter"

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
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

func readUntil(startSequence []byte, stopSequence []byte) []byte {
	buffer := make([]byte, 250)
	preamble := make([]byte, 0, 1024)
	result := make([]byte, 0, 1024)

	// First scan for start delimiter
	for !bytes.Contains(preamble, startSequence) {
		c, err := port.Read(buffer)
		if err != nil {
			log.Printf("Read error: %v", err)
			return nil
		}
		logDebug("Read %d bytes", c)
		logDebug("%x", buffer[:c])
		preamble = append(preamble, buffer[:c]...)
		logDebug("appended preamble is now %d bytes", len(preamble))
	}
	startIndex := bytes.Index(preamble, startSequence)
	log.Printf("Start sequence begins at byte %d of preamble", startIndex)
	result = preamble[startIndex:]
	log.Printf("Starting result with %d initial bytes from the preamble", len(result))

	// Scan for termination sequence, keep all on between
	for !bytes.Contains(result, stopSequence) {
		c, err := port.Read(buffer)
		if err != nil {
			log.Printf("Read error: %v", err)
			return nil
		}
		logDebug("Read %d bytes", c)
		logDebug(hex.EncodeToString(buffer[:c]))
		if bytes.Contains(buffer[:c], stopSequence) {
			idx := bytes.Index(buffer[:c], stopSequence)
			result = append(result, buffer[:idx+len(stopSequence)]...)
			log.Printf("Last read contained delimiter at %d, skipping %d bytes, returning result with %d bytes", idx, c-idx, len(result))
			return result
		}
		result = append(result, buffer[:c]...)
		logDebug("appended result is now %d bytes", len(result))
	}

	finalStopIdx := bytes.Index(result, stopSequence)
	return result[:finalStopIdx+len(stopSequence)]
}

func gatherData(iteration int) bool {
	timer := prometheus.NewTimer(gatheringDuration.WithLabelValues(options.MeterName))
	defer timer.ObserveDuration()

	if !options.KeepAlive {
		port = openConnection()
		defer closeConnection()
	}

	log.Println("Gathering metrics")
	message := readUntil(mustDecodeStringToHex("1b1b1b1b01010101"), mustDecodeStringToHex("1b1b1b1b1a"))
	if message == nil {
		log.Printf("Failed to read message, skipping")
		return false
	}
	logDebug("Read full message %s", hex.EncodeToString(message))

	for _, meterReading := range extractMeterReadings(message) {
		log.Printf("Recording meter %s with value %f", meterReading.name, meterReading.value)
		gaugeReading.WithLabelValues(options.MeterName, meterReading.name).Set(meterReading.value)
		publishData(meterReading, iteration)
	}
	return true
}

func mustDecodeStringToHex(data string) []byte {
	res, err := hex.DecodeString(data)
	if err != nil {
		log.Panicf("Decoding static %s to hex failed", data)
	}
	return res
}

func extractMeterReadings(message []byte) []meterReading {
	result := make([]meterReading, 0, 5)
	// split on list start and obis prefix
	dataSplice := bytes.Split(message, mustDecodeStringToHex("77070100"))
	for _, data := range dataSplice[1:] {
		logDebug("Decoding message %x", data)
		if len(data) < 12 {
			log.Printf("Data chunk too small, %d<12", len(data))
			continue
		}
		obis := fmt.Sprintf("%d.%d.%d", data[0], data[1], data[2])
		logDebug("Decoded obis %s", obis)
		// split on unit defintion (Wh)
		dataSplice2 := bytes.Split(data, mustDecodeStringToHex("621e"))

		if len(dataSplice2) < 2 {
			logDebug("Skipping obis entry without expected unit")
			continue
		}
		data = dataSplice2[1]
		logDebug("Decoding 2nd part of message %x", data)
		size := data[2] & 0x0f

		if size > 0 {
			logDebug("Decoded size %d", size)
			value := float64(decodeBytes(data[3:3+size-1])) / float64(options.Factor)
			logDebug("Decoded value %f", value)
			newReading := meterReading{name: obis, value: value}
			result = append(result, newReading)
		} else {
			logDebug("Skipping message because of undecoded size")
		}
	}
	return result
}

func decodeBytes(raw []byte) int64 {

	logDebug("Decoding bytes %x", raw)
	buffer := make([]byte, 8)
	sizeDiff := len(buffer) - len(raw)
	if sizeDiff > 0 {
		for index, value := range raw {
			buffer[sizeDiff+index] = value
		}
	} else {
		buffer = raw[0-sizeDiff:]
	}

	return int64(binary.BigEndian.Uint64(buffer))
}

func logDebug(format string, v ...interface{}) {
	if options.Debug {
		log.Debugf(format, v...)
	}
}

// 010800ff 65 00 0101 8001 621e 5203 69 000000000000000001
// 010801ff 01 01 621e 5203 69 000000000000000001
// 020800ff 01 01 621e 5203 69 000000000000000201
