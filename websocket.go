package websocket

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"
	json "github.com/json-iterator/go"
	"go.uber.org/atomic"
	"gopkg.in/errgo.v2/errors"
)

const (
	defaultWait       = time.Minute * 3
	defaultPingPeriod = time.Second * 5
)

var closedErr = errors.New("has been closed.")

type Option func(c *Client)

func WithDialTimeout(timeout time.Duration) Option { return func(c *Client) { c.timeout = timeout } }
func WithPingPeriod(period time.Duration) Option   { return func(c *Client) { c.pingPeriod = period } }
func WithPing(msg IMessage) Option                 { return func(c *Client) { c.ping = msg } }
func WithLogger(logger *log.Logger) Option         { return func(c *Client) { c.log = logger } }
func WithLoggerOptions(opts *log.Options) Option   { return func(c *Client) { c.logOpts = opts } }
func WithLoggerWriter(writer io.Writer) Option     { return func(c *Client) { c.logWriter = writer } }
func WithOnConnected(f func(*Client)) Option       { return func(c *Client) { c.onConnected = f } }
func WithPrefix(prefix string) Option              { return func(c *Client) { c.prefix = prefix } }

type msgManager struct {
	mutex sync.RWMutex
	data  map[IMessage]struct{}
}

func (m *msgManager) clear() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.data = make(map[IMessage]struct{}, 0)
}

func (m *msgManager) update(message IMessage, subscribe bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if subscribe {
		m.data[message] = struct{}{}
	} else {
		delete(m.data, message)
	}
}

func (m *msgManager) getData() map[IMessage]struct{} {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	temp := make(map[IMessage]struct{})
	for msg := range m.data {
		temp[msg] = struct{}{}
	}
	return temp
}

type Client struct {
	URL        string
	ping       IMessage
	timeout    time.Duration
	pingPeriod time.Duration
	conn       *websocket.Conn
	processor  IReceiver
	log        *log.Logger
	logOpts    *log.Options
	logWriter  io.Writer

	onConnected func(client *Client)

	subscr chan IMessage
	unsub  chan IMessage
	wchan  chan struct{}
	rchan  chan struct{}
	cchan  chan struct{}
	status atomic.Value
	mutex  sync.RWMutex
	mmer   *msgManager

	ctx    context.Context
	prefix string
}

func NewClient(ctx context.Context, url string, receiver IReceiver, opts ...Option) *Client {
	wc := &Client{
		URL:       url,
		ctx:       ctx,
		processor: receiver,
		subscr:    make(chan IMessage, 5),
		unsub:     make(chan IMessage),
		wchan:     make(chan struct{}),
		rchan:     make(chan struct{}),
		cchan:     make(chan struct{}),
		mmer:      &msgManager{data: make(map[IMessage]struct{})},
	}
	wc.status.Store(StatusWaiting)
	wc.getOpts(opts...)
	if wc.log == nil {
		if len(wc.prefix) > 0 {
			wc.prefix += "  "
		}
		prefix := wc.prefix + wc.URL
		logOpts := wc.logOpts
		if logOpts == nil {
			logOpts = &log.Options{
				ReportCaller:    true,
				ReportTimestamp: true,
				TimeFormat:      time.DateTime,
				Prefix:          prefix,
			}
		} else {
			prefix = logOpts.Prefix
			if len(prefix) > 0 {
				prefix += "  "
			}
			prefix += wc.URL
			logOpts.Prefix = prefix
		}
		writer := wc.logWriter
		if writer == nil {
			writer = os.Stdout
		}
		wc.log = log.NewWithOptions(writer, *logOpts)
	}
	if r, ok := receiver.(interface {
		SetLogger(*log.Logger)
	}); ok {
		r.SetLogger(wc.log)
	}
	return wc
}

func (wc *Client) getOpts(opts ...Option) {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(wc)
	}
}

func durationDefault(v, d time.Duration) time.Duration {
	if v == 0 {
		return d
	}
	return v
}

func (wc *Client) Shutdown() error {
	wc.mutex.Lock()
	defer wc.mutex.Unlock()

	if wc.status.Load() == StatusDisconnected {
		return nil
	}

	wc.status.Store(StatusDisconnecting)
	wc.log.Info("closing websocket connection")
	wc.mmer.clear()
	wc.status.Store(StatusDisconnected)
	close(wc.subscr)
	err := wc.conn.Close()
	if err != nil {
		return err
	}
	wc.cchan <- struct{}{}
	<-wc.rchan
	<-wc.wchan
	wc.log.Info("websocket connection has be closed")
	return nil
}

func (wc *Client) Connect() error {
	err := wc.connect(false)
	if err == nil && wc.onConnected != nil {
		wc.onConnected(wc)
	}
	return err
}

func (wc *Client) connect(reconnect bool) error {
	wc.mutex.Lock()
	defer wc.mutex.Unlock()

	status := wc.status.Load()
	if status == StatusDisconnected {
		return closedErr
	} else if reconnect {
		wc.status.Store(StatusReConnecting)
	} else if status == StatusConnected {
		return nil
	} else {
		wc.status.Store(StatusConnecting)
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), durationDefault(wc.timeout, defaultWait))
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, wc.URL, nil)
	if err != nil {
		wc.log.Error("connect", "err", err)
		return err
	}

	wc.status.Store(StatusConnected)

	wc.conn = conn
	if !reconnect {
		go wc.accept()
		go wc.writePump()
	}
	return nil
}

func (wc *Client) reconnect() {
	err := wc.connect(true)
	if err != nil {
		if err == closedErr {
			wc.log.Warn("websocket closed, cancel reconnect...")
			return
		}

		time.Sleep(time.Second)
		wc.reconnect()
		return
	}
	wc.log.Warn("websocket reconnected")

	if wc.onConnected != nil {
		wc.onConnected(wc)
	}
	wc.resendMessages()
}

func (wc *Client) resendMessages() {
	for msg := range wc.mmer.getData() {
		err := wc.Subscribe(msg)
		if err != nil {
			if wc.status.Load() == StatusDisconnected {
				return
			}

			time.Sleep(time.Second)
			wc.resendMessages()
		}
	}
}

func (wc *Client) accept() {
	defer func() {
		if err := recover(); err != nil {
			wc.log.Error("accept", "err", err)
		}
		wc.log.Info("stopped acceptor...")
		wc.rchan <- struct{}{}
	}()

	for {
		mt, r, err := wc.conn.NextReader()
		if err != nil {
			switch wc.status.Load() {
			case StatusDisconnected:
				return
			case StatusReConnecting, StatusConnecting, StatusWaiting:
				time.Sleep(time.Second)
				continue
			}

			wc.log.Error("read message", "type", getMessageType(mt), "err", err)
			if mt == -1 || strings.Contains(err.Error(), "use of closed network connection") ||
				websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
				websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				wc.reconnect()
			}

			continue
		}

		wc.processor.OnReceive(&Frame{
			Type:   mt,
			Reader: r,
		})
	}
}

// getMessageType 1:TextMessage 2:BinaryMessage 8:CloseMessage 9:PingMessage 10:PongMessage
func getMessageType(t int) string {
	switch t {
	default:
		return "TextMessage"
	case 2:
		return "BinaryMessage"
	case 8:
		return "CloseMessage"
	case 9:
		return "PingMessage"
	case 10:
		return "PongMessage"
	}
}

func (wc *Client) writePump() {
	period := durationDefault(wc.pingPeriod, defaultPingPeriod)
	ticker := time.NewTicker(period)
	defer func() {
		ticker.Stop()
		wc.log.Info("stopped writer...")
		wc.wchan <- struct{}{}
	}()

	for {
		select {
		case <-ticker.C:
			switch wc.status.Load() {
			case StatusDisconnected:
				return
			case StatusReConnecting, StatusDisconnecting,
				StatusConnecting, StatusWaiting:
				time.Sleep(period)
				continue
			}

			if wc.ping == nil {
				ticker.Stop()
				continue
			}

			wc.subscr <- wc.ping
		case <-wc.cchan:
			return
		case msg, ok := <-wc.subscr:
			wc.writeMessage(msg, ok, true)
		case msg, ok := <-wc.unsub:
			wc.writeMessage(msg, ok, false)
		case <-wc.ctx.Done():
			err := wc.Shutdown()
			if err != nil {
				wc.log.Error("shutdown websocket", "err", err)
			}
		}
	}
}

func (wc *Client) writeMessage(msg IMessage, ok bool, subscribe bool) {
	if !ok {
		return
	}

	wc.mmer.update(msg, subscribe)
	var buf []byte
	var err error
	if sm, ok := msg.(*StringMessage); ok {
		buf = []byte(*sm)
	} else {
		buf, err = json.Marshal(msg)
		if err != nil {
			wc.log.Error("encode message", "message", msg, "err", err)
			return
		}
	}

	err = wc.conn.WriteMessage(websocket.TextMessage, buf)
	if err != nil {
		wc.log.Error("send message", "err", err)
		return
	}
	if msg.IsPing() {
		wc.log.Debug("send [ping]", "message", string(buf))
	} else {
		wc.log.Info("send", "message", string(buf))
	}
}

func (wc *Client) Subscribe(message IMessage) error {
	return wc.send(true, message)
}

func (wc *Client) Unsubscribe(message IMessage) error {
	return wc.send(false, message)
}

func (wc *Client) send(subscribe bool, message IMessage) error {
	switch wc.status.Load() {
	case StatusDisconnected:
		return fmt.Errorf("websocket disconnected: %+v", message)
	case StatusReConnecting, StatusConnecting, StatusWaiting:
		return fmt.Errorf("websocket connecting: %+v", message)
	case StatusDisconnecting:
		return fmt.Errorf("websocket disconecting: %+v", message)
	}
	if subscribe {
		wc.subscr <- message
	} else {
		wc.unsub <- message
	}
	return nil
}
