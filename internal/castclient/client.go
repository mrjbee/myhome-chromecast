package castclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vishen/go-chromecast/cast"
	pb "github.com/vishen/go-chromecast/cast/proto"
)

const (
	defaultSender      = "sender-0"
	defaultReceiver    = "receiver-0"
	connectionNS       = "urn:x-cast:com.google.cast.tp.connection"
	receiverNS         = "urn:x-cast:com.google.cast.receiver"
	mediaNS            = "urn:x-cast:com.google.cast.media"
	youtubeNS          = "urn:x-cast:com.google.youtube.mdx"
	youtubeReceiverID  = "233637DE"
	youtubeLaunchDelay = 20 * time.Second
	youtubeStopDelay   = 10 * time.Second
	youtubeMDXRetry    = time.Second
)

type mdxRequest struct {
	Type string `json:"type"`
}

func (*mdxRequest) SetRequestId(int) {}

type stopAppPayload struct {
	cast.PayloadHeader
	SessionID string `json:"sessionId"`
}

type setVolumePayload struct {
	cast.PayloadHeader
	Volume struct {
		Level float32 `json:"level"`
	} `json:"volume"`
}

type screenStatus struct {
	transportID string
	screenID    string
}

type Client struct {
	conn    *cast.Connection
	timeout time.Duration
	logger  *slog.Logger
	nextID  atomic.Int64

	waitMu  sync.Mutex
	waiters map[int]chan *pb.CastMessage
	done    chan struct{}
	screens chan screenStatus
	close   sync.Once
}

func Dial(ctx context.Context, address string, port int, timeout time.Duration, logger *slog.Logger) (*Client, cast.ReceiverStatusResponse, error) {
	client := &Client{
		conn:    cast.NewConnection(),
		timeout: timeout,
		logger:  logger,
		waiters: make(map[int]chan *pb.CastMessage),
		done:    make(chan struct{}),
		screens: make(chan screenStatus, 8),
	}
	client.nextID.Store(time.Now().UnixNano() % 1_000_000_000)
	if err := client.conn.Start(address, port); err != nil {
		return nil, cast.ReceiverStatusResponse{}, err
	}
	go client.receive()

	connect := &cast.PayloadHeader{Type: "CONNECT"}
	if err := client.send(connect, defaultReceiver, connectionNS); err != nil {
		_ = client.Close()
		return nil, cast.ReceiverStatusResponse{}, fmt.Errorf("initialize Cast connection: %w", err)
	}
	status, err := client.ReceiverStatus(ctx)
	if err != nil {
		_ = client.Close()
		return nil, cast.ReceiverStatusResponse{}, fmt.Errorf("get initial receiver status: %w", err)
	}
	return client, status, nil
}

func (c *Client) Close() error {
	var err error
	c.close.Do(func() {
		closePayload := &cast.PayloadHeader{Type: "CLOSE"}
		_ = c.send(closePayload, defaultReceiver, connectionNS)
		err = c.conn.Close()
	})
	return err
}

func (c *Client) receive() {
	defer close(c.done)
	for message := range c.conn.MsgChan() {
		if message == nil || message.PayloadUtf8 == nil {
			continue
		}
		var header struct {
			Type      string `json:"type"`
			RequestID int    `json:"requestId"`
		}
		if err := json.Unmarshal([]byte(message.GetPayloadUtf8()), &header); err != nil {
			continue
		}
		if header.RequestID != 0 {
			c.waitMu.Lock()
			waiter := c.waiters[header.RequestID]
			c.waitMu.Unlock()
			if waiter != nil {
				select {
				case waiter <- message:
				default:
				}
			}
		}
		if message.GetNamespace() == youtubeNS && header.Type == "mdxSessionStatus" {
			var payload struct {
				Data struct {
					ScreenID string `json:"screenId"`
				} `json:"data"`
			}
			if json.Unmarshal([]byte(message.GetPayloadUtf8()), &payload) == nil && payload.Data.ScreenID != "" {
				select {
				case c.screens <- screenStatus{transportID: message.GetSourceId(), screenID: payload.Data.ScreenID}:
				default:
				}
			}
		}
	}
}

func (c *Client) send(payload cast.Payload, destination, namespace string) error {
	id := int(c.nextID.Add(1))
	payload.SetRequestId(id)
	return c.conn.Send(id, payload, defaultSender, destination, namespace)
}

func (c *Client) sendAndWait(ctx context.Context, payload cast.Payload, destination, namespace string) (*pb.CastMessage, error) {
	id := int(c.nextID.Add(1))
	payload.SetRequestId(id)
	waiter := make(chan *pb.CastMessage, 1)
	c.waitMu.Lock()
	c.waiters[id] = waiter
	c.waitMu.Unlock()
	defer func() {
		c.waitMu.Lock()
		delete(c.waiters, id)
		c.waitMu.Unlock()
	}()

	if err := c.conn.Send(id, payload, defaultSender, destination, namespace); err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case message := <-waiter:
		return message, nil
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	case <-c.done:
		return nil, errors.New("Cast connection closed")
	}
}

func (c *Client) ReceiverStatus(ctx context.Context) (cast.ReceiverStatusResponse, error) {
	message, err := c.sendAndWait(ctx, &cast.PayloadHeader{Type: "GET_STATUS"}, defaultReceiver, receiverNS)
	if err != nil {
		return cast.ReceiverStatusResponse{}, err
	}
	var status cast.ReceiverStatusResponse
	if err := json.Unmarshal([]byte(message.GetPayloadUtf8()), &status); err != nil {
		return cast.ReceiverStatusResponse{}, fmt.Errorf("decode receiver status: %w", err)
	}
	return status, nil
}

func (c *Client) SetVolume(ctx context.Context, level float32) (cast.Volume, error) {
	if level < 0 || level > 1 {
		return cast.Volume{}, errors.New("volume level must be between 0 and 1")
	}
	payload := &setVolumePayload{PayloadHeader: cast.PayloadHeader{Type: "SET_VOLUME"}}
	payload.Volume.Level = level
	message, err := c.sendAndWait(ctx, payload, defaultReceiver, receiverNS)
	if err != nil {
		return cast.Volume{}, err
	}
	var status cast.ReceiverStatusResponse
	if err := json.Unmarshal([]byte(message.GetPayloadUtf8()), &status); err != nil {
		return cast.Volume{}, fmt.Errorf("decode volume response: %w", err)
	}
	return status.Status.Volume, nil
}

func (c *Client) MediaCommand(ctx context.Context, command string) error {
	status, err := c.ReceiverStatus(ctx)
	if err != nil {
		return err
	}
	app, err := activeApplication(status)
	if err != nil {
		return err
	}
	if err := c.send(&cast.PayloadHeader{Type: "CONNECT"}, app.TransportId, connectionNS); err != nil {
		return err
	}
	message, err := c.sendAndWait(ctx, &cast.PayloadHeader{Type: "GET_STATUS"}, app.TransportId, mediaNS)
	if err != nil {
		return fmt.Errorf("get media status: %w", err)
	}
	var mediaStatus cast.MediaStatusResponse
	if err := json.Unmarshal([]byte(message.GetPayloadUtf8()), &mediaStatus); err != nil {
		return fmt.Errorf("decode media status: %w", err)
	}
	if len(mediaStatus.Status) == 0 {
		return errors.New("receiver has no active media session")
	}

	commandType := map[string]string{"play": "PLAY", "pause": "PAUSE", "stop": "STOP"}[command]
	if commandType == "" {
		return fmt.Errorf("unsupported media command %q", command)
	}
	payload := &cast.MediaHeader{
		PayloadHeader:  cast.PayloadHeader{Type: commandType},
		MediaSessionId: mediaStatus.Status[0].MediaSessionId,
	}
	if _, err := c.sendAndWait(ctx, payload, app.TransportId, mediaNS); err != nil {
		return fmt.Errorf("send media command: %w", err)
	}
	return nil
}

func (c *Client) YouTubeScreenID(ctx context.Context) (string, error) {
	status, err := c.ReceiverStatus(ctx)
	if err != nil {
		return "", err
	}
	if app, ok := youtubeApplication(status); ok {
		c.logger.Info("reusing existing YouTube receiver session",
			"sessionId", app.SessionId,
			"transportId", app.TransportId,
		)
		screenID, screenErr := c.requestYouTubeScreenID(ctx, app.TransportId)
		if screenErr == nil {
			return screenID, nil
		}
		if !errors.Is(screenErr, context.DeadlineExceeded) || ctx.Err() != nil {
			return "", screenErr
		}
		c.logger.Warn("existing YouTube receiver session did not return screen ID; restarting it",
			"sessionId", app.SessionId,
			"transportId", app.TransportId,
			"error", screenErr,
		)
		if err := c.stopYouTubeReceiver(ctx, app); err != nil {
			return "", fmt.Errorf("recover YouTube receiver: %w", err)
		}
	}

	app, err := c.launchYouTubeReceiver(ctx)
	if err != nil {
		return "", err
	}
	return c.requestYouTubeScreenID(ctx, app.TransportId)
}

func (c *Client) requestYouTubeScreenID(ctx context.Context, transportID string) (string, error) {
	if err := c.send(&cast.PayloadHeader{Type: "CONNECT"}, transportID, connectionNS); err != nil {
		return "", fmt.Errorf("connect to YouTube transport: %w", err)
	}
	for {
		select {
		case <-c.screens:
		default:
			goto drained
		}
	}

drained:
	waitCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	retry := time.NewTicker(youtubeMDXRetry)
	defer retry.Stop()

	requestStatus := func() error {
		if err := c.send(&mdxRequest{Type: "getMdxSessionStatus"}, transportID, youtubeNS); err != nil {
			return fmt.Errorf("request YouTube MDX status: %w", err)
		}
		return nil
	}
	if err := requestStatus(); err != nil {
		return "", err
	}
	for {
		select {
		case screen := <-c.screens:
			if screen.transportID != transportID {
				c.logger.Warn("received YouTube screen ID from unexpected transport",
					"expectedTransportId", transportID,
					"sourceTransportId", screen.transportID,
				)
			}
			return screen.screenID, nil
		case <-retry.C:
			if err := requestStatus(); err != nil {
				return "", err
			}
		case <-waitCtx.Done():
			return "", fmt.Errorf("wait for YouTube screen ID: %w", waitCtx.Err())
		case <-c.done:
			return "", errors.New("Cast connection closed")
		}
	}
}

func (c *Client) stopYouTubeReceiver(ctx context.Context, app cast.Application) error {
	if app.SessionId == "" {
		return errors.New("running YouTube receiver has no session ID")
	}
	stopCtx, cancel := context.WithTimeout(ctx, youtubeStopDelay)
	defer cancel()
	if err := c.send(&stopAppPayload{
		PayloadHeader: cast.PayloadHeader{Type: "STOP"},
		SessionID:     app.SessionId,
	}, defaultReceiver, receiverNS); err != nil {
		return fmt.Errorf("stop YouTube receiver: %w", err)
	}

	for {
		status, err := c.ReceiverStatus(stopCtx)
		if err == nil {
			if _, running := youtubeApplication(status); !running {
				c.logger.Info("stopped unresponsive YouTube receiver session", "sessionId", app.SessionId)
				return nil
			}
		}
		select {
		case <-stopCtx.Done():
			return fmt.Errorf("wait for YouTube receiver to stop: %w", stopCtx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (c *Client) launchYouTubeReceiver(ctx context.Context) (cast.Application, error) {
	launchCtx, cancel := context.WithTimeout(ctx, youtubeLaunchDelay)
	defer cancel()
	c.logger.Info("launching YouTube receiver", "appId", youtubeReceiverID)
	if err := c.send(&cast.LaunchRequest{
		PayloadHeader: cast.PayloadHeader{Type: "LAUNCH"},
		AppId:         youtubeReceiverID,
	}, defaultReceiver, receiverNS); err != nil {
		return cast.Application{}, fmt.Errorf("launch YouTube receiver: %w", err)
	}

	for {
		status, err := c.ReceiverStatus(launchCtx)
		if err == nil {
			if app, running := youtubeApplication(status); running {
				c.logger.Info("YouTube receiver launched",
					"sessionId", app.SessionId,
					"transportId", app.TransportId,
				)
				return app, nil
			}
		}
		select {
		case <-launchCtx.Done():
			return cast.Application{}, fmt.Errorf("wait for YouTube receiver: %w", launchCtx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func youtubeApplication(status cast.ReceiverStatusResponse) (cast.Application, bool) {
	for _, app := range status.Status.Applications {
		if app.AppId == youtubeReceiverID && app.TransportId != "" {
			return app, true
		}
	}
	return cast.Application{}, false
}

func activeApplication(status cast.ReceiverStatusResponse) (cast.Application, error) {
	for index := len(status.Status.Applications) - 1; index >= 0; index-- {
		app := status.Status.Applications[index]
		if app.TransportId != "" && !app.IsIdleScreen {
			return app, nil
		}
	}
	return cast.Application{}, errors.New("receiver has no active application")
}
