package websocket

import "io"

type OnConnectedHandler func(*Client)

type ReConnectedHandler func(*Client)

type DecompressHandler func(io.Reader) (io.Reader, error)

type OnCloseHandler func(code int, text string) error

type PingHandler func(appData string) error

type PongHandler func(appData string) error

type ErrorHandler func(FrameType, error)

type HeartbeatHandler func(*Client)

type Receiver interface {
	Unmarshal(frameType FrameType, reader io.Reader) (any, error)
	OnMessage(any)
}
