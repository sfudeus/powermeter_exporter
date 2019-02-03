package main // import "github.com/sfudeus/powermeter_exporter"

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/jacobsa/go-serial/serial"
	"github.com/jessevdk/go-flags"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

var port io.ReadWriteCloser

var options struct {
	Port      int64  `long:"port" default:"8080" description:"The address to listen on for HTTP requests." env:"EXPORTER_PORT"`
	Interval  int64  `long:"interval" default:"60" env:"INTERVAL" description:"The frequency in seconds in which to gather data"`
	Device    string `long:"device" default:"/dev/irmeter0" description:"The device to read on"`
	MeterName string `long:"metername" description:"The name of your meter, to uniquely name them if you have multiple"`
	Factor    int64  `long:"factor" description:"Reduction factor for all readings" default:"1"`
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

	go func() {
		port = openConnection()
		defer port.Close()
		for {
			ok := gatherData()
			if !ok {
				log.Printf("Data Gathering failed, resetting port")
				port.Close()
				port = openConnection()
			}
			time.Sleep(time.Duration(options.Interval) * time.Second)
		}
	}()
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", options.Port), nil))
}

func openConnection() io.ReadWriteCloser {
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
	port, err := serial.Open(options)
	if err != nil {
		log.Fatalf("serial.Open: %v", err)
	}
	return port
}

func readUntil(hexNeedle string) []byte {
	buffer := make([]byte, 250)
	result := make([]byte, 0, 1024)
	targetBytes, err := hex.DecodeString(hexNeedle)
	if err != nil {
		log.Fatalf("Failed to decode target string: %v", err)
	}

	//TODO wait for start delimiter 
	for !bytes.Contains(result, targetBytes) {
		c, err := port.Read(buffer)
		if err != nil {
			log.Printf("Read error: %v", err)
			return nil
		}
		log.Printf("Read %d bytes", c)
		log.Println(hex.EncodeToString(buffer[:c]))
		if bytes.Contains(buffer, targetBytes) {
			idx := bytes.Index(buffer, targetBytes)
			result = append(result, buffer[:idx]...)
			log.Printf("Last read contained delimiter at %d, skipping %d bytes, returning result with %d bytes", idx, c-idx, len(result))
			return result
		}
		result = append(result, buffer[:c]...)
		log.Printf("appended result is now %d bytes", len(result))
	}

	return result
}

func gatherData() bool {
	timer := prometheus.NewTimer(gatheringDuration.WithLabelValues(options.MeterName))
	defer timer.ObserveDuration()

	log.Println("Gathering metrics")
	message := readUntil("1b1b1b1b1a")
	if message == nil {
		log.Printf("Failed to read message, skipping")
		return false
	}
	log.Printf("Read full message %s", hex.EncodeToString(message))

	for _, meterReading := range extractMeterReadings(message) {
		log.Printf("Recording meter %s with value %f", meterReading.name, meterReading.value)
		gaugeReading.WithLabelValues(options.MeterName, meterReading.name).Set(meterReading.value)
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
	dataSplice := bytes.Split(message, mustDecodeStringToHex("77070100"))
	for _, data := range dataSplice[1:] {
		log.Printf("Decoding message %x", data)
		if len(data) < 12 {
			log.Printf("Data chunk too small, %d<12", len(data))
			continue
		}
		obis := fmt.Sprintf("%d.%d.%d", data[0], data[1], data[2])
		log.Printf("Decoded obis %s", obis)
		size := mapByteCount(data[10])
		if size > 0 {
			log.Printf("Decoded size %d", size)
			value := float64(decodeBytes(data[11:11+size])) / float64(options.Factor)
			log.Printf("Decoded value %f", value)
			newReading := meterReading{name: obis, value: value}
			result = append(result, newReading)
		} else {
			log.Print("Skipping message because of undecoded size")
		}
	}
	return result
}

func mapByteCount(sizeInfo byte) int {
	switch sizeInfo {
	case '\x55':
		return 4
	case '\x56':
		return 5
	}
	log.Printf("Tried to decode unknown sizeInfo: %x", sizeInfo)
	return 0
}

func decodeBytes(raw []byte) int64 {

	log.Printf("Decoding bytes %x", raw)
	buffer := make([]byte, 8)
	sizeDiff := len(buffer) - len(raw)
	for index, value := range raw {
		buffer[sizeDiff+index] = value
	}
	return int64(binary.BigEndian.Uint64(buffer))
}
