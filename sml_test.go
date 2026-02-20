package main

import (
	"bufio"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
)

type fakePort struct {
	data   [][]byte
	readIx int
}

func (f *fakePort) Read(p []byte) (int, error) {
	if f.readIx >= len(f.data) {
		return 0, io.EOF
	}
	copy(p, f.data[f.readIx])
	n := len(f.data[f.readIx])
	f.readIx++
	return n, nil
}

func (f *fakePort) Write(p []byte) (int, error) { return 0, nil }
func (f *fakePort) Close() error                { return nil }

func TestMain(m *testing.M) {
	options.Factor = 1
	options.MaxValue = 10000000
	options.Debug = true
	log.SetLevel(log.DebugLevel)

	os.Exit(m.Run())
}

func prepareTestdata(filename string, t *testing.T) *fakePort {
	file, err := os.Open(filename)
	if err != nil {
		t.Fatalf("could not open testdata: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	hexdata := ""
	for scanner.Scan() {
		hexdata += strings.TrimSpace(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("error reading testdata: %v", err)
	}
	data, err := hex.DecodeString(hexdata)
	if err != nil {
		t.Fatalf("could not decode hex: %v", err)
	}

	// Simuliere Port-Lesevorgänge in 32-Byte-Blöcken
	var chunks [][]byte
	for i := 0; i < len(data); i += 32 {
		end := i + 32
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}
	return &fakePort{data: chunks}
}

func TestNormalRead(t *testing.T) {

	port := prepareTestdata("testdata/smlfile-1", t)

	msg, err := readMessage(port)
	if err != nil {
		t.Fatalf("readMessage failed: %v", err)
	}
	if len(msg) == 0 {
		t.Error("readMessage returned empty result")
	}
}

func TestExtractListResponse(t *testing.T) {

	port := prepareTestdata("testdata/smlfile-1", t)
	msg, err := readMessage(port)
	if err != nil {
		t.Fatalf("readMessage failed: %v", err)
	}
	smlListResponse, err := extractListResponse(msg)
	if err != nil {
		t.Fatalf("extractListResponse failed: %v", err)
	}
	if len(smlListResponse) == 0 {
		t.Error("extractListResponse returned empty result")
	}
}

func TestExtractMeterReadings(t *testing.T) {

	port := prepareTestdata("testdata/smlfile-1", t)
	msg, err := readMessage(port)
	if err != nil {
		t.Fatalf("readMessage failed: %v", err)
	}
	smlListResponse, err := extractListResponse(msg)
	if err != nil {
		t.Fatalf("extractListResponse failed: %v", err)
	}
	readings := extractMeterReadings(smlListResponse)
	if len(readings) != 4 {
		t.Error("extractMeterReadings returned wrong amount of results")
	}
}
