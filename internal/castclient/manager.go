package castclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vishen/go-chromecast/cast"

	"myhome-chromecast/internal/discovery"
	"myhome-chromecast/internal/youtube"
)

var (
	ErrNotConnected              = errors.New("no Cast device is connected")
	ErrConnectedDeviceNotPresent = errors.New("connected device is no longer present in discovery registry")
)

type Connected struct {
	Device discovery.DeviceView `json:"device"`
}

type activeConnection struct {
	device discovery.Device
	client *Client
}

type Manager struct {
	mu          sync.Mutex
	registry    *discovery.Registry
	youtube     *youtube.Client
	castTimeout time.Duration
	logger      *slog.Logger
	active      *activeConnection
}

func NewManager(registry *discovery.Registry, youtubeClient *youtube.Client, castTimeout time.Duration, logger *slog.Logger) *Manager {
	return &Manager{registry: registry, youtube: youtubeClient, castTimeout: castTimeout, logger: logger}
}

func (m *Manager) Connect(ctx context.Context, id string) (Connected, error) {
	device, ok := m.registry.Get(id)
	if !ok {
		return Connected{}, fmt.Errorf("device %q not found", id)
	}
	address := device.Entry.GetAddr()
	if address == "" || device.Entry.Port == 0 {
		return Connected{}, errors.New("discovered device has no usable address or port")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		reason := "switch"
		if m.active.device.Entry.UUID == id {
			reason = "reconnect"
		}
		_ = m.closeActiveLocked(reason)
	}

	m.logger.Info("connecting to Cast device",
		"uuid", device.Entry.UUID,
		"name", device.Entry.DeviceName,
		"address", address,
		"port", device.Entry.Port,
	)
	client, _, err := Dial(ctx, address, device.Entry.Port, m.castTimeout)
	if err != nil {
		m.logger.Warn("failed to connect to Cast device",
			"uuid", device.Entry.UUID,
			"name", device.Entry.DeviceName,
			"address", address,
			"port", device.Entry.Port,
			"error", err,
		)
		return Connected{}, err
	}
	m.active = &activeConnection{device: device, client: client}
	m.logger.Info("connected to Cast device",
		"uuid", device.Entry.UUID,
		"name", device.Entry.DeviceName,
		"address", address,
		"port", device.Entry.Port,
	)
	return Connected{Device: device.View()}, nil
}

func (m *Manager) Connected() (Connected, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.activeClientLocked(); err != nil {
		return Connected{}, err
	}
	return Connected{Device: m.active.device.View()}, nil
}

func (m *Manager) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeActiveLocked("request")
}

func (m *Manager) Status(ctx context.Context) (cast.ReceiverStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	client, err := m.activeClientLocked()
	if err != nil {
		return cast.ReceiverStatusResponse{}, err
	}
	return client.ReceiverStatus(ctx)
}

func (m *Manager) SetVolume(ctx context.Context, level float32) (cast.Volume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	client, err := m.activeClientLocked()
	if err != nil {
		return cast.Volume{}, err
	}
	return client.SetVolume(ctx, level)
}

func (m *Manager) MediaCommand(ctx context.Context, command string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	client, err := m.activeClientLocked()
	if err != nil {
		return err
	}
	return client.MediaCommand(ctx, command)
}

func (m *Manager) PlayYouTube(ctx context.Context, rawURL string) (string, error) {
	videoID, err := youtube.ParseVideoID(rawURL)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	client, err := m.activeClientLocked()
	if err != nil {
		return "", err
	}
	screenID, err := client.YouTubeScreenID(ctx)
	if err != nil {
		return "", err
	}
	if err := m.youtube.Play(ctx, screenID, videoID); err != nil {
		return "", err
	}
	return videoID, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeActiveLocked("shutdown")
}

func (m *Manager) activeClientLocked() (*Client, error) {
	if m.active == nil {
		return nil, ErrNotConnected
	}
	current, ok := m.registry.Get(m.active.device.Entry.UUID)
	if !ok {
		return nil, ErrConnectedDeviceNotPresent
	}
	m.active.device = current
	return m.active.client, nil
}

func (m *Manager) closeActiveLocked(reason string) error {
	if m.active == nil {
		return nil
	}
	device := m.active.device
	err := m.active.client.Close()
	m.active = nil
	if err != nil {
		m.logger.Warn("failed to disconnect from Cast device",
			"uuid", device.Entry.UUID,
			"name", device.Entry.DeviceName,
			"reason", reason,
			"error", err,
		)
		return err
	}
	m.logger.Info("disconnected from Cast device",
		"uuid", device.Entry.UUID,
		"name", device.Entry.DeviceName,
		"reason", reason,
	)
	return nil
}
