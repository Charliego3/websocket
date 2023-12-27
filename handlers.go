package websocket

import "io"

type OnConnectedHandler func(*Client)

type ReConnectedHandler func(*Client)

type DecompressHandler func(io.Reader) (io.Reader, error)

type ErrorHandler func(FrameType, error)

type Receiver interface {
	Unmarshal(frameType FrameType, reader io.Reader) (any, error)
	OnMessage(any)
}
