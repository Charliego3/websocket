package websocket

import (
	"github.com/charmbracelet/log"
	"io"
)

type Frame struct {
	Type   MessageType
	Reader io.Reader
}

type IMessage interface {
	IsPing() bool
}

func NewStringMessage(msg string) *StringMessage {
	sm := new(StringMessage)
	*sm = StringMessage(msg)
	return sm
}

type JsonMessage struct{}
type StringMessage string

func (j *JsonMessage) IsPing() bool   { return false }
func (s *StringMessage) IsPing() bool { return true }

type IWebsocket interface {
	Connect() error
	Shutdown() error
	SendMessage(message IMessage) error
}

type Receiver interface {
	OnReceive(frame *Frame)
}

type LoggedReceiver struct {
	logger *log.Logger
}

func (r *LoggedReceiver) OnReceive(frame *Frame) {
	bs, err := io.ReadAll(frame.Reader)
	if err != nil {
		r.logger.Error("received message, but read fail", "error", err)
		return
	}
	r.logger.Info("received", "type", frame.Type.String(), "message", string(bs))
}

func (r *LoggedReceiver) SetLogger(logger *log.Logger) {
	r.logger = logger
}

func (r *LoggedReceiver) Logger() *log.Logger {
	return r.logger
}
