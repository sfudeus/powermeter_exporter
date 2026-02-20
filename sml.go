package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	log "github.com/sirupsen/logrus"
)

// Examples of the messages we want to parse, in hex:
// 010800ff 65 00 0101 8001 621e 5203 69 000000000000000001
// 010801ff 01 01 621e 5203 69 000000000000000001
// 020800ff 01 01 621e 5203 69 000000000000000201

var SML_ESCAPE = "1b1b1b1b"
var SML_FILE_START = "01010101"
var SML_FILE_END = "1a"
var SML_LIST_RESPONSE_PREFIX = "7263070177"
var SML_OBIS_PREFIX = "77070100"
var SML_UNIT_WATT_HOUR = "621e"

func readMessage(port io.ReadWriteCloser) ([]byte, error) {
	return readUntil(port, mustDecodeStringToHex(SML_ESCAPE+SML_FILE_START), mustDecodeStringToHex(SML_ESCAPE+SML_FILE_END))
}

func readUntil(port io.ReadWriteCloser, startSequence []byte, stopSequence []byte) ([]byte, error) {
	buffer := make([]byte, 250)
	preamble := make([]byte, 0, 1024)
	result := make([]byte, 0, 1024)

	// First scan for start delimiter
	for !bytes.Contains(preamble, startSequence) {
		c, err := port.Read(buffer)
		if err != nil {
			log.Printf("Read error searching for startSequence: %v", err)
			return nil, err
		}
		logDebug("Read %d bytes from port", c)
		logDebug("%x", buffer[:c])
		preamble = append(preamble, buffer[:c]...)
		logDebug("appended preamble is now %d bytes", len(preamble))
	}
	startIndex := bytes.Index(preamble, startSequence)
	log.Printf("Start sequence begins at byte %d of preamble", startIndex)
	result = preamble[startIndex:]
	log.Printf("Starting result with %d initial bytes from the preamble, after throwing away %d bytes", len(result), startIndex)

	// Scan for stop sequence, keep all in between
	for !bytes.Contains(result, stopSequence) {
		c, err := port.Read(buffer)
		if err != nil {
			log.Printf("Read error searching for stopSequence: %v", err)
			return nil, err
		}
		logDebug("Read %d additional bytes in search for stopSequence", c)
		logDebug("%s", hex.EncodeToString(buffer[:c]))

		if bytes.Contains(buffer[:c], stopSequence) {
			idx := bytes.Index(buffer[:c], stopSequence)
			result = append(result, buffer[:idx+len(stopSequence)]...)
			log.Printf("Last read contained stopSequence at %d, skipping %d bytes, returning result with %d bytes", idx, c-idx, len(result))
			return result, nil
		}
		result = append(result, buffer[:c]...)
		logDebug("appended result is now %d bytes", len(result))
	}

	finalStopIdx := bytes.Index(result, stopSequence)
	logDebug("Stop sequence found in result at idx %d, finalizing message", finalStopIdx)
	return result[:finalStopIdx+len(stopSequence)], nil
}

func extractListResponse(smlFile []byte) ([]byte, error) {
	// split on list response prefix
	dataSplice := bytes.Split(smlFile, mustDecodeStringToHex(SML_LIST_RESPONSE_PREFIX))
	if len(dataSplice) != 2 {
		return nil, errors.New("Failed to find single list response prefix in message")
	}
	logDebug("Decoding smlListResponse\n%s", formatHexBytes(dataSplice[1], 32))
	return dataSplice[1], nil
}

func extractMeterReadings(smlListResponse []byte) []meterReading {
	result := make([]meterReading, 0, 5)
	// split on list start and obis prefix
	dataSplice := bytes.Split(smlListResponse, mustDecodeStringToHex(SML_OBIS_PREFIX))
	for _, data := range dataSplice[1:] {
		logDebug("Decoding obis chunk\n%s", formatHexBytes(data, 32))
		if len(data) < 12 {
			log.Printf("Data chunk too small, %d<12", len(data))
			continue
		}
		obis := fmt.Sprintf("%d.%d.%d", data[0], data[1], data[2])
		logDebug("Decoded obis %s", obis)
		// split on unit defintion (Wh)
		dataSplice2 := bytes.Split(data, mustDecodeStringToHex(SML_UNIT_WATT_HOUR))

		if len(dataSplice2) < 2 {
			logDebug("Skipping obis entry %s without expected unit", obis)
			continue
		}
		data = dataSplice2[1]
		logDebug("Decoding 2nd part of message\n%s", formatHexBytes(data, 32))
		scaler := data[1] & 0x0f // e.g. 5203 // expect 3 for kWh
		size := data[2] & 0x0f   // e.g. 69xxxxxxxxx

		if size > 0 {
			logDebug("Decoded size %d", size)
			logDebug("Decoded scaler %d", scaler)
			value := float64(decodeBytes(data[3:3+size-1])) / float64(options.Factor)
			logDebug("Decoded value %f", value)
			if value < float64(options.MaxValue) && value > 0 {
				newReading := meterReading{name: obis, value: value}
				result = append(result, newReading)
			} else {
				log.Infof("Skipped value %f for obis %s because implausible or 0", value, obis)
			}
		} else {
			logDebug("Skipping message because of undecoded size")
		}
	}
	return result
}

/*
1b 1b 1b 1b                               # Escape-Sequenz
01 01 01 01                               # Beginn der Nachricht
76                                        # StartSMLMessage (Nachricht 1/3)
  0b 45 53 59 41 88 2e 11 6c b3 48        #   transactionId
  62 00                                   #   groupNo (not set)
  62 00                                   #   abortOnError (no error)
  72                                      #   messageBody (Liste)
    63 01 01                              #     OpenResponse (01 01)
    76                                    #     Payload (Liste)
      01                                  #       codepage
      04 45 53 59                         #       clientId
      08 45 53 59 e6 6e b3 48             #       reqFileId
      0b XX XX XX XX XX XX XX XX XX XX    #       serverId (*)
      01                                  #       refTime (secIndex)
      01                                  #       smlVersion
  63 dd 8e                                #   crc16
  00                                      #   EndOfSmlMsg
76                                        # StartSMLMessage (Nachricht 2/3)
  0b 45 53 59 41 88 2e 11 6c b3 49        #   transactionId
  62 00                                   #   groupNo (not set)
  62 00                                   #   abortOnError (no error)
  72                                      #   messageBody (Liste)
    63 07 01                              #     GetListResponse (07 01)
    77                                    #       Payload (Liste)
      01                                  #       clientId
      0b XX XX XX XX XX XX XX XX XX XX    #       serverId (*)
      08 01 00 62 0a ff ff 00             #       listName
      72                                  #       actSensorTime (Liste)
        62 01                             #         secIndex
        65 05 ce e6 6e                    #         97445486 Sek seit Einbau
      74                                  #       valList
        77                                #         1. OBIS Nachricht
          07 81 81 c7 82 03 ff            #           objName=Hersteller-ID
          01                              #           status
          01                              #           valTime
          01                              #           unit
          01                              #           scaler
          04 45 53 59                     #           value='ESY' (Easymeter)
          01                              #           valueSignature
        77                                #         2. OBIS Nachricht
          07 01 00 00 00 09 ff            #           objName=serverId (OBIS 1-0:0.0.9)
          01                              #           status
          01                              #           valTime
          01                              #           unit
          01                              #           scaler
          0b XX XX XX XX XX XX XX XX XX XX#           value (*)
          01                              #           valueSignature
        77                                #         3. OBIS Nachricht
          07 01 00 01 08 00 ff            #           objName=Zählerstand (OBIS 1-0:1.8.0)
          64 00 00 80                     #           status=??
          01                              #           valTime
          62 1e                           #           unit=Wattstunden (DLMS 30)
          52 03                           #           scaler=3 (value*10^3)
          59 00 00 00 00 00 00 22 a0      #           value=8864
          01                              #           valueSignature
        77                                #         4. OBIS Nachricht
          07 81 81 c7 f0 06 ff            #           objName=Konfiguration
          01                              #           status
          01                              #           valTime
          01                              #           unit
          01                              #           scaler
          04 31 07 0e                     #           value (Konfig der Herstellers)
          01                              #           valueSignature
      01                                  #       listSignature
      01                                  #       actGatewayTime
  63 0d b1                                #   crc16
  00                                      #   EndOfSmlMsg
76                                        # StartSMLMessage (Nachricht 3/3)
  0b 45 53 59 41 88 2e 11 6c b3 4a        #   transactionId
  62 00                                   #   groupNo (not set)
  62 00                                   #   abortOnError (no error)
  72                                      #   messageBody (Liste)
    63 02 01                              #     CloseResponse (02 01)
    71                                    #     Payload
      01                                  #       globalSignature
  63 71 b7                                #   crc16
  00                                      # EndOfSmlMsg
00                                        # Füll-Byte
00                                        # Füll-Byte
1b 1b 1b 1b                               # Escape-Sequenz
1a                                        # Ende der Nachricht
02                                        # Anzahl der Füll-Bytes
29 10                                     # Prüfsumme
*/
