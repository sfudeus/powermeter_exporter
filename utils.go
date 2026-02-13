package main

import (
	"encoding/binary"
	"encoding/hex"
	"strings"

	log "github.com/sirupsen/logrus"
)

func mustDecodeStringToHex(data string) []byte {
	res, err := hex.DecodeString(data)
	if err != nil {
		log.Panicf("Decoding static %s to hex failed", data)
	}
	return res
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

func formatHexBytes(data []byte, maxLineLen int) string {
	hexStr := hex.EncodeToString(data)
	var sb strings.Builder
	for i := 0; i < len(hexStr); i += maxLineLen {
		end := i + maxLineLen
		end = min(end, len(hexStr))
		sb.WriteString(" " + hexStr[i:end] + "\n")
	}
	return sb.String()
}
