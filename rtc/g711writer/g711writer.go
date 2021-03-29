// Package g711Writer implements OGG media container writer
package g711writer

import (
	"errors"
	"os"

	"github.com/pion/rtp"
)

var (
	errInvalidNilPacket = errors.New("invalid nil packet")
)

// G711Writer is used to take RTP packets and write them to a G711 file on disk
type G711Writer struct {
	fd           *os.File
	sampleRate   uint32
	channelCount uint16
}

// New builds a new G711 writer
func New(fileName string, sampleRate uint32, channelCount uint16) (*G711Writer, error) {
	f, err := os.Create(fileName)
	if err != nil {
		return nil, err
	}
	writer := &G711Writer{
		fd:           f,
		sampleRate:   sampleRate,
		channelCount: channelCount,
	}
	return writer, nil
}

// WriteRTP adds a new packet
func (i *G711Writer) WriteRTP(packet *rtp.Packet) error {
	if packet == nil {
		return errInvalidNilPacket
	}
	_, err := i.fd.Write(packet.Payload)
	return err
}

// Close stops the recording
func (i *G711Writer) Close() error {
	var err error
	if i.fd != nil {
		err = i.fd.Close()
		i.fd = nil
	}
	return err
}
