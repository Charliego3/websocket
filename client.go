package websocket

import (
	"context"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	mutex sync.Mutex
	conns = make(map[string]*Client)
)

type clientOpts struct {
	logger        *slog.Logger
	dialer        *websocket.Dialer
	proxyURL      string
	header        http.Header
	compression   bool
	autoReconnect bool

	readTimeout    time.Duration
	writeTimeout   time.Duration
	connectTimeout time.Duration

	onConnected   OnConnectedHandler
	onReconnected ReConnectedHandler
	onMessage     OnMessageHandler
	decompress    DecompressHandler
	errHandler    ErrorHandler

	parser Parser
}

func newOpts() *clientOpts {
	opts := new(clientOpts)
	opts.logger = slog.Default()
	opts.readTimeout = time.Second * 30
	opts.writeTimeout = time.Second * 3
	opts.connectTimeout = time.Second * 30
	opts.onConnected = func(*Client) {}
	opts.onReconnected = func(*Client) {}
	opts.onMessage = func(Frame) {}
	opts.parser = new(ReaderParser)
	return opts
}

type Client struct {
	*clientOpts
	ctx    context.Context
	wsURL  string
	conn   *websocket.Conn
	status Status
	stopC  chan struct{}
}

func NewClient(ctx context.Context, wsURL string, opts ...Option[Client]) (*Client, error) {
	if c, ok := conns[wsURL]; ok {
		return c, nil
	}

	mutex.Lock()
	defer mutex.Unlock()

	if c, ok := conns[wsURL]; ok {
		return c, nil
	}

	client := new(Client)
	client.ctx = ctx
	client.clientOpts = newOpts()
	client.wsURL = wsURL
	client.errHandler = client.defaultErrorHandler
	applyOpts[Client](client, opts)
	client.status = StatusConnecting
	client.stopC = make(chan struct{})
	if err := client.connect(); err != nil {
		return nil, err
	}
	client.status = StatusConnected
	client.onConnected(client)
	conns[wsURL] = client
	client.logger.Info("Websocket connected", slog.String("URL", wsURL))
	return client, nil
}

func (o *Client) connect() (err error) {
	dialer, err := o.getDialer()
	if err != nil {
		return err
	}

	o.conn, _, err = dialer.Dial(o.wsURL, o.header)
	if err != nil {
		return
	}

	go o.readLoop()
	return
}

func (o *Client) reconnect() {
	o.status = StatusReConnecting
	if err := o.connect(); err != nil {
		return
	}

	o.status = StatusConnected
	o.onReconnected(o)
}

func (o *Client) setWriteDeadLine() {
	_ = o.conn.SetWriteDeadline(time.Now().Add(o.writeTimeout))
}

func (o *Client) SendMessage(message []byte) error {
	o.setWriteDeadLine()
	return o.conn.WriteMessage(websocket.TextMessage, message)
}

func (o *Client) SendJson(message any) error {
	o.setWriteDeadLine()
	return o.conn.WriteJSON(message)
}

func (o *Client) SendPing(message []byte) error {
	o.setWriteDeadLine()
	return o.conn.WriteMessage(websocket.PingMessage, message)
}

func (o *Client) SendPong(message []byte) error {
	o.setWriteDeadLine()
	return o.conn.WriteMessage(websocket.PongMessage, message)
}

func (o *Client) SendClose(message []byte) error {
	o.setWriteDeadLine()
	return o.conn.WriteMessage(websocket.CloseMessage, message)
}

func (o *Client) readLoop() {
	//o.conn.SetPingHandler(nil)
	//o.conn.SetPongHandler(func(appData string) error {
	//	return nil
	//})

	readFrame := func() bool {
		defer func() {
			if err := recover(); err != nil {
				o.logger.Error("failed to process received message",
					slog.Any("err", err))
			}
		}()

		select {
		case <-o.stopC:
			o.logger.Info("websocket accept is stopped")
			return true
		case <-o.ctx.Done():
			o.logger.Info("received stop command")
			return true
		default:
			_ = o.conn.SetReadDeadline(time.Now().Add(o.readTimeout))
			ft, reader, err := o.conn.NextReader()
			if err != nil {
				if o.autoReconnect {
					o.reconnect()
				} else {
					_ = o.conn.Close()
				}
				return true
			}

			frameType := FrameType(ft)
			if frameType != FrameTypeText && frameType != FrameTypeBinary {
				return false
			}

			if frameType == FrameTypeBinary && o.decompress != nil {
				if reader, err = o.decompress(reader); err != nil {
					o.errHandler(frameType, errors.Wrap(err, "failed to decompress frame"))
					return false
				}
			}

			frame, per := o.parser.Parse(frameType, reader)
			if per != nil {
				o.errHandler(frameType, errors.Wrap(per, "failed to parse frame"))
				return false
			}
			o.onMessage(frame)
			return false
		}
	}

	for {
		if readFrame() {
			return
		}
	}
}

func (o *Client) defaultErrorHandler(frameType FrameType, err error) {
	o.logger.Error("websocket got an error",
		slog.String("URL", o.wsURL),
		slog.String("frameType", frameType.String()),
		slog.Any("err", err),
	)
}

func (o *clientOpts) getDialer() (*websocket.Dialer, error) {
	if o.dialer == nil {
		o.dialer = &websocket.Dialer{
			Proxy:             http.ProxyFromEnvironment,
			HandshakeTimeout:  o.connectTimeout,
			EnableCompression: o.compression,
		}

		if o.proxyURL != "" {
			proxy, err := url.Parse(o.proxyURL)
			if err != nil {
				return nil, err
			}
			o.dialer.Proxy = http.ProxyURL(proxy)
		}
	}

	return o.dialer, nil
}
