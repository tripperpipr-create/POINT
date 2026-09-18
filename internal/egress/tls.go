package egress

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const maxClientHelloBytes = 64 * 1024

// readClientHello returns the exact TLS records for forwarding plus the SNI.
// It never terminates TLS and therefore cannot observe HTTP paths or bodies.
func readClientHello(reader io.Reader) ([]byte, string, error) {
	raw := make([]byte, 0, 4096)
	handshake := make([]byte, 0, 4096)
	expected := 0
	for len(raw) < maxClientHelloBytes && (expected == 0 || len(handshake) < expected) {
		header := make([]byte, 5)
		if _, err := io.ReadFull(reader, header); err != nil {
			return nil, "", err
		}
		if header[0] != 22 || header[1] != 3 {
			return nil, "", errors.New("egress accepts only a TLS ClientHello")
		}
		length := int(binary.BigEndian.Uint16(header[3:5]))
		if length < 1 || length > 18*1024 || len(raw)+5+length > maxClientHelloBytes {
			return nil, "", errors.New("TLS ClientHello record exceeds the safe limit")
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, "", err
		}
		raw = append(raw, header...)
		raw = append(raw, payload...)
		handshake = append(handshake, payload...)
		if len(handshake) >= 4 && expected == 0 {
			if handshake[0] != 1 {
				return nil, "", errors.New("first TLS handshake message is not ClientHello")
			}
			expected = 4 + int(handshake[1])<<16 + int(handshake[2])<<8 + int(handshake[3])
			if expected > maxClientHelloBytes {
				return nil, "", errors.New("TLS ClientHello exceeds the safe limit")
			}
		}
	}
	if expected == 0 || len(handshake) < expected {
		return nil, "", errors.New("incomplete TLS ClientHello")
	}
	serverName, err := clientHelloServerName(handshake[4:expected])
	if err != nil {
		return nil, "", err
	}
	return raw, serverName, nil
}

func clientHelloServerName(body []byte) (string, error) {
	position := 2 + 32
	if len(body) < position+1 {
		return "", errors.New("truncated TLS ClientHello")
	}
	position += 1 + int(body[position])
	if len(body) < position+2 {
		return "", errors.New("truncated TLS cipher suites")
	}
	position += 2 + int(binary.BigEndian.Uint16(body[position:position+2]))
	if len(body) < position+1 {
		return "", errors.New("truncated TLS compression methods")
	}
	position += 1 + int(body[position])
	if len(body) < position+2 {
		return "", errors.New("TLS ClientHello has no extensions")
	}
	extensionsLength := int(binary.BigEndian.Uint16(body[position : position+2]))
	position += 2
	if extensionsLength < 0 || position+extensionsLength > len(body) {
		return "", errors.New("truncated TLS extensions")
	}
	end := position + extensionsLength
	for position+4 <= end {
		typeID := binary.BigEndian.Uint16(body[position : position+2])
		length := int(binary.BigEndian.Uint16(body[position+2 : position+4]))
		position += 4
		if position+length > end {
			return "", errors.New("truncated TLS extension")
		}
		if typeID != 0 {
			position += length
			continue
		}
		data := body[position : position+length]
		if len(data) < 5 || int(binary.BigEndian.Uint16(data[:2])) != len(data)-2 || data[2] != 0 {
			return "", errors.New("invalid TLS server_name extension")
		}
		nameLength := int(binary.BigEndian.Uint16(data[3:5]))
		if nameLength < 1 || 5+nameLength > len(data) {
			return "", errors.New("invalid TLS server_name length")
		}
		name, err := normalizeFQDN(string(data[5 : 5+nameLength]))
		if err != nil {
			return "", fmt.Errorf("invalid TLS SNI: %w", err)
		}
		return name, nil
	}
	return "", errors.New("TLS ClientHello does not declare SNI")
}
