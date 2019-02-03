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
	gaugeWork = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "powermeter",
		Name:      "work",
		Help:      "Current meter reading for consumed energy (in Wh)",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
			//id of the meter, like 1.8.1 for consumed electrical energy, first tariff
			"meter_id",
		})
	gaugePower = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "powermeter",
		Name:      "power",
		Help:      "Current power consumption (in W)",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
		})
)

func main() {
	_, err := flags.Parse(&options)
	if err != nil {
		os.Exit(1)
	}

	go func() {
		port = openConnection()
		defer port.Close()
		for {
			gatherData()
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
	buffer := make([]byte, 500)
	result := make([]byte, 0, 500)
	targetBytes, err := hex.DecodeString(hexNeedle)
	if err != nil {
		log.Fatalf("Failed to decode target string: %v", err)
	}

	for !bytes.Contains(result, targetBytes) {
		c, err := port.Read(buffer)
		if err != nil {
			log.Fatalf("Read error: %v", err)
		}
		log.Printf("Read %d bytes", c)
		log.Println(hex.EncodeToString(buffer[:c]))
		result = append(result, buffer[:c]...)
		log.Printf("appended result is now %d bytes", len(result))
	}

	return result
}

func gatherData() {
	log.Println("Gathering metrics")
	message := readUntil("1b1b1b1b1a")
	log.Printf("Read full message %s", hex.EncodeToString(message))
	work0 := extractData(message, mustDecodeStringToHex("77070100010800ff0101621e52ff56"), 15, 5)
	work1 := extractData(message, mustDecodeStringToHex("77070100010801ff0101621e52ff56"), 15, 5)
	work2 := extractData(message, mustDecodeStringToHex("77070100010802ff0101621e52ff56"), 15, 5)
	power := extractData(message, mustDecodeStringToHex("77070100100700ff0101621b52ff55"), 15, 4)
	log.Printf("Work0 is %d, Work1 is %d, Work2 is %d, Power is %d", work0, work1, work2, power)
	log.Println("Done gathering metrics")
	gaugeWork.WithLabelValues(options.MeterName, "1.8.0").Set(float64(work0))
	gaugeWork.WithLabelValues(options.MeterName, "1.8.1").Set(float64(work1))
	gaugeWork.WithLabelValues(options.MeterName, "1.8.2").Set(float64(work2))
	gaugePower.WithLabelValues(options.MeterName).Set(float64(power))
}

func mustDecodeStringToHex(data string) []byte {
	res, err := hex.DecodeString(data)
	if err != nil {
		log.Panicf("Decoding static %s to hex failed", data)
	}
	return res
}

func extractData(message []byte, needle []byte, offset int, length int) int64 {
	index := bytes.Index(message, needle)
	if index >= 0 {
		data := message[index+offset : index+offset+length]
		log.Printf("Decoding data %v", hex.EncodeToString(data))
		return decodeBytes(data) / options.Factor
	}
	return 0
}

func decodeBytes(raw []byte) int64 {

	buffer := make([]byte, 8)
	sizeDiff := len(buffer) - len(raw)
	for index, value := range raw {
		buffer[sizeDiff+index] = value
	}
	return int64(binary.BigEndian.Uint64(buffer))
}
