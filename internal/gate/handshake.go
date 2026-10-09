// Package gate is the public entry point for servers reachable from the internet.
// It reads the Minecraft handshake, lets through only hostnames of public servers,
// rewrites them to the private name and hands the connection to mc-router.
package gate

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

const (
	// The handshake is small: a few varints, a host of at most 255 chars and a port.
	maxHandshake = 1024
	maxHostLen   = 255
)

var (
	ErrLegacyPing   = errors.New("legacy server list ping is not supported")
	ErrBadHandshake = errors.New("malformed handshake")
)

// Handshake is the first packet a Java client sends.
type Handshake struct {
	Protocol  int32
	Host      string // without the suffix
	Suffix    string // "\x00..." added by Forge or proxies, kept as is
	Port      uint16
	NextState int32
}

// readVarInt reads a Minecraft VarInt (at most 5 bytes).
func readVarInt(r io.ByteReader) (int32, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int32(v), nil
		}
	}
	return 0, ErrBadHandshake
}

func appendVarInt(buf []byte, v int32) []byte {
	u := uint32(v)
	for {
		if u&^0x7F == 0 {
			return append(buf, byte(u))
		}
		buf = append(buf, byte(u&0x7F|0x80))
		u >>= 7
	}
}

// ReadHandshake reads and parses the handshake packet from r.
func ReadHandshake(r *bufio.Reader) (Handshake, error) {
	first, err := r.Peek(1)
	if err != nil {
		return Handshake{}, err
	}
	if first[0] == 0xFE {
		return Handshake{}, ErrLegacyPing
	}
	length, err := readVarInt(r)
	if err != nil {
		return Handshake{}, err
	}
	if length <= 0 || length > maxHandshake {
		return Handshake{}, ErrBadHandshake
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return Handshake{}, err
	}
	br := bytes.NewReader(body)
	id, err := readVarInt(br)
	if err != nil || id != 0 {
		return Handshake{}, ErrBadHandshake
	}
	var hs Handshake
	if hs.Protocol, err = readVarInt(br); err != nil {
		return Handshake{}, ErrBadHandshake
	}
	n, err := readVarInt(br)
	if err != nil || n < 0 || int(n) > maxHostLen*4 || int(n) > br.Len() {
		return Handshake{}, ErrBadHandshake
	}
	host := make([]byte, n)
	if _, err := io.ReadFull(br, host); err != nil {
		return Handshake{}, ErrBadHandshake
	}
	if err := binary.Read(br, binary.BigEndian, &hs.Port); err != nil {
		return Handshake{}, ErrBadHandshake
	}
	if hs.NextState, err = readVarInt(br); err != nil {
		return Handshake{}, ErrBadHandshake
	}
	s := string(host)
	if i := strings.IndexByte(s, 0); i >= 0 {
		s, hs.Suffix = s[:i], s[i:]
	}
	hs.Host = strings.ToLower(strings.TrimSuffix(s, "."))
	return hs, nil
}

// Encode writes the handshake as a packet, with Host and Suffix joined again.
func (hs Handshake) Encode() []byte {
	addr := hs.Host + hs.Suffix
	body := appendVarInt(nil, 0)
	body = appendVarInt(body, hs.Protocol)
	body = appendVarInt(body, int32(len(addr)))
	body = append(body, addr...)
	body = binary.BigEndian.AppendUint16(body, hs.Port)
	body = appendVarInt(body, hs.NextState)
	return append(appendVarInt(nil, int32(len(body))), body...)
}
