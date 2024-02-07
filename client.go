package websocket

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

var (
	mutex sync.Mutex
	conns = make(map[string]*Client)
)

type clientOpts struct {
	logger           Logger
	dialer           *websocket.Dialer
	proxyURL         string
	header           http.Header
	compression      bool
	compressionLevel int
	autoReconnect    bool
	readLimit        int64

	readTimeout       time.Duration
	writeTimeout      time.Duration
	connectTimeout    time.Duration
	heartbeatInterval time.Duration
	delayReconnect    time.Duration

	onConnected     OnConnectedHandler
	onReconnected   ReConnectedHandler
	decompress      DecompressHandler
	onClose         OnCloseHandler
	pingHandler     PingHandler
	pongHandler     PongHandler
	errHandler      ErrorHandler
	heartbeat       HeartbeatHandler
	beforeReconnect BeforeReconnectHandler
}

func newOpts() *clientOpts {
	opts := new(clientOpts)
	opts.logger = slog.Default()
	opts.readTimeout = time.Second * 3
	opts.writeTimeout = time.Second * 3
	opts.connectTimeout = time.Second * 30
	opts.delayReconnect = time.Second
	opts.heartbeatInterval = time.Minute
	opts.onConnected = func(*Client) {}
	opts.onReconnected = func(*Client) {}
	opts.beforeReconnect = func(*Client) {}
	return opts
}

type Message struct {
	Type FrameType
	Msg  []byte
}

type Client struct {
	*clientOpts
	ctx      context.Context
	wsURL    string
	conn     *websocket.Conn
	stopC    chan struct{}
	writeC   chan Message
	receiver Receiver
	sdOnce   sync.Once
	status   Status
	mutex    sync.Mutex
}

func (c *Client) URL() string {
	return c.wsURL
}

func NewClient(ctx context.Context, URL string, receiver Receiver, opts ...Option[Client]) (*Client, error) {
	if c, ok := conns[URL]; ok {
		return c, nil
	}
	if receiver == nil {
		return nil, errors.New("can't using nil receiver")
	}
	mutex.Lock()
	defer mutex.Unlock()

	if c, ok := conns[URL]; ok {
		return c, nil
	}

	client := new(Client)
	client.ctx = ctx
	client.clientOpts = newOpts()
	client.receiver = receiver
	client.wsURL = URL
	client.errHandler = client.defaultErrorHandler
	applyOpts[Client](client, opts)
	client.writeC = make(chan Message, 10)
	client.status = StatusConnecting
	if err := client.connect(); err != nil {
		return nil, err
	}
	client.status = StatusConnected
	client.onConnected(client)
	conns[URL] = client
	client.logger.Info("Websocket connected", slog.String("URL", URL))
	return client, nil
}

func (c *Client) setStatus(status Status) {
	atomic.StoreUint32((*uint32)(&c.status), uint32(status))
}

func (c *Client) Status() Status {
	return Status(atomic.LoadUint32((*uint32)(&c.status)))
}

func (c *Client) Shutdown() (err error) {
	c.sdOnce.Do(func() {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		c.setStatus(StatusDisconnecting)
		close(c.stopC)
		delete(conns, c.wsURL)
		if c.conn != nil {
			err = c.conn.Close()
		}
		c.setStatus(StatusDisconnected)
		c.logger.Info("Websocket stopped.", slog.String("URL", c.wsURL))
	})
	return
}

func (c *Client) connect() (err error) {
	if c.dialer == nil {
		c.dialer = &websocket.Dialer{
			Proxy:             http.ProxyFromEnvironment,
			HandshakeTimeout:  c.connectTimeout,
			EnableCompression: c.compression,
		}

		if c.proxyURL != "" {
			proxy, err := url.Parse(c.proxyURL)
			if err != nil {
				return err
			}
			c.dialer.Proxy = http.ProxyURL(proxy)
		}
	}

	c.conn, _, err = c.dialer.Dial(c.wsURL, c.header)
	if err != nil {
		return
	}

	if err = c.conn.SetCompressionLevel(c.compressionLevel); err != nil {
		return err
	}
	c.conn.SetReadLimit(c.readLimit)
	if c.onClose != nil {
		c.conn.SetCloseHandler(c.onClose)
	}
	if c.pingHandler != nil {
		c.conn.SetPingHandler(c.pingHandler)
	}
	if c.pongHandler != nil {
		c.conn.SetPongHandler(c.pongHandler)
	}

	c.stopC = make(chan struct{})
	go c.readLoop()
	go c.writeLoop()
	return
}

func (c *Client) reconnect() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	status := c.Status()
	if status == StatusDisconnecting || status == StatusDisconnected {
		return
	}

	close(c.stopC)
	_ = c.conn.Close()

	c.logger.Info("Websocket reconnecting", slog.String("URL", c.wsURL))
	c.beforeReconnect(c)
	c.setStatus(StatusReConnecting)
	for {
		if err := c.connect(); err != nil {
			c.errHandler(FrameTypeNoFrame, EventReconnect, err)
			time.Sleep(c.delayReconnect)
			continue
		} else {
			break
		}
	}
	c.setStatus(StatusConnected)
	c.onReconnected(c)
	c.logger.Info("Websocket reconnected", slog.String("URL", c.wsURL))
}

func (c *Client) getWriteDeadline() time.Time {
	return time.Now().Add(c.writeTimeout)
}

func (c *Client) SendMessage(message []byte) {
	c.writeC <- Message{Type: FrameTypeText, Msg: message}
}

func (c *Client) SendBinary(message []byte) {
	c.writeC <- Message{Type: FrameTypeBinary, Msg: message}
}

func (c *Client) SendJson(message any) error {
	bs, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.writeC <- Message{Type: FrameTypeText, Msg: bs}
	return nil
}

func (c *Client) SendPing(message []byte) {
	c.writeC <- Message{Type: FrameTypePing, Msg: message}
}

func (c *Client) SendPong(message []byte) {
	c.writeC <- Message{Type: FrameTypePong, Msg: message}
}

func (c *Client) SendClose(message []byte) {
	c.writeC <- Message{Type: FrameTypeClose, Msg: message}
}

func (c *Client) writeLoop() {
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopC:
			return
		case <-c.ctx.Done():
			_ = c.Shutdown()
			return
		case <-ticker.C:
			if c.heartbeat != nil {
				c.heartbeat(c)
			}
		case msg := <-c.writeC:
			var err error
			switch msg.Type {
			case FrameTypePing, FrameTypePong, FrameTypeClose:
				err = c.conn.WriteControl(int(msg.Type), msg.Msg, c.getWriteDeadline())
			case FrameTypeText, FrameTypeBinary:
				_ = c.conn.SetWriteDeadline(c.getWriteDeadline())
				err = c.conn.WriteMessage(int(msg.Type), msg.Msg)
			default:
				err = errors.New("cannot send no frame message")
			}

			if err != nil {
				c.errHandler(msg.Type, EventWrite, err)
				break
			}

			if l, ok := c.logger.(interface {
				Debug(string, ...any)
			}); ok {
				l.Debug("Websocket send", slog.String("message", string(msg.Msg)))
			}
		}
	}
}

func (c *Client) readLoop() {
	readFrame := func() bool {
		frameType := FrameTypeNoFrame
		defer func() {
			if err := recover(); err != nil {
				c.errHandler(frameType, EventRead, errors.Errorf("%v", err))
			}
		}()

		select {
		case <-c.stopC:
			return true
		case <-c.ctx.Done():
			_ = c.Shutdown()
			return true
		default:
			_ = c.conn.SetReadDeadline(time.Now().Add(c.readTimeout))
			ft, reader, err := c.conn.NextReader()
			frameType = FrameType(ft)
			if err != nil {
				c.errHandler(frameType, EventRead, err)
				if c.autoReconnect {
					go c.reconnect()
				} else {
					_ = c.Shutdown()
				}
				return true
			}

			if frameType == FrameTypeBinary && c.decompress != nil {
				if reader, err = c.decompress(reader); err != nil {
					c.errHandler(frameType, EventDecompress, err)
					return false
				}
			}

			frame, err := c.receiver.Unmarshal(frameType, reader)
			if err != nil {
				c.errHandler(frameType, EventUnmarshal, err)
				return false
			}
			c.receiver.OnMessage(frame)
			return false
		}
	}

	for {
		if readFrame() {
			return
		}
	}
}

func (c *Client) defaultErrorHandler(frameType FrameType, event Event, err error) {
	c.logger.Error("Websocket got an error",
		slog.String("URL", c.wsURL),
		slog.String("frameType", frameType.String()),
		slog.String("event", event.String()),
		slog.Any("err", err),
	)
}
