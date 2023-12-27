package websocket

import "io"

type OnConnectedHandler func(*Client)

type ReConnectedHandler func(*Client)

type OnMessageHandler func(Frame)

type DecompressHandler func(io.Reader) (io.Reader, error)

type ErrorHandler func(FrameType, error)

type Parser interface {
	Parse(frameType FrameType, reader io.Reader) (Frame, error)
}

type ReaderParser struct{}

func (r *ReaderParser) Parse(frameType FrameType, reader io.Reader) (Frame, error) {
	return &ReaderFrame{Reader: reader, Type: frameType}, nil
}

type Frame interface {
	Payload() any
}

type ReaderFrame struct {
	io.Reader
	Type FrameType
}

func (r *ReaderFrame) Payload() any {
	return r.Reader
}

type BytesFrame []byte

func (b *BytesFrame) Payload() any {
	return *b
}

func NewBytesFrame(bytes []byte) *BytesFrame {
	frame := new(BytesFrame)
	*frame = bytes
	return frame
}
